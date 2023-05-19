//go:build e2e
// +build e2e

/*
Copyright 2021 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package metadata_e2e

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
)

var tr *runner.TestRunner

const (
	// Number of get calls before starting tests.
	numHealthChecks = 60
	appName         = "metadataapp" // App name in Dapr.
)

type mockMetadata struct {
	ID                   string                    `json:"id"`
	ActiveActorsCount    []activeActorsCount       `json:"actors"`
	Extended             map[string]string         `json:"extended"`
	RegisteredComponents []mockRegisteredComponent `json:"components"`
}

type activeActorsCount struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

type mockRegisteredComponent struct {
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
}

func testSetMetadata(t *testing.T, metadataAppExternalURL string) {
	t.Log("Setting sidecar metadata")
	url := fmt.Sprintf("%s/test/setMetadata", metadataAppExternalURL)
	resp, err := utils.HTTPPost(url, []byte(`{"key":"newkey","value":"newvalue"}`))
	require.NoError(t, err)
	require.NotEmpty(t, resp, "response must not be empty!")
}

func testGetMetadata(t *testing.T, metadataAppExternalURL string) {
	t.Log("Getting sidecar metadata")
	url := fmt.Sprintf("%s/test/getMetadata", metadataAppExternalURL)
	resp, err := utils.HTTPGet(url)
	require.NoError(t, err)
	require.NotEmpty(t, resp, "response must not be empty!")
	var metadata mockMetadata
	err = json.Unmarshal(resp, &metadata)
	require.NoError(t, err)
	for _, comp := range metadata.RegisteredComponents {
		require.NotEmpty(t, comp.Name, "component name must not be empty")
		require.NotEmpty(t, comp.Type, "component type must not be empty")
		require.True(t, len(comp.Capabilities) >= 0, "component capabilities key must be present!")
	}
	require.NotEmpty(t, metadata.Extended)
	require.NotEmpty(t, metadata.Extended["daprRuntimeVersion"])
	require.Equal(t, "newvalue", metadata.Extended["newkey"])
}

func TestMain(m *testing.M) {
	utils.SetupLogs(appName)
	utils.InitHTTPClient(true)

	// These apps will be deployed before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:        appName,
			DaprEnabled:    true,
			ImageName:      "e2e-metadata",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
		},
	}

	log.Printf("Creating TestRunner\n")
	tr = runner.NewTestRunner("metadatatest", testApps, nil, nil)
	log.Printf("Starting TestRunner\n")
	os.Exit(tr.Start(m))
}

func TestMetadata(t *testing.T) {
	t.Log("Enter TestMetadataHTTP")
	metadataAppExternalURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, metadataAppExternalURL, "metadataAppExternalURL must not be empty!")

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(metadataAppExternalURL, numHealthChecks)
	require.NoError(t, err)

	testSetMetadata(t, metadataAppExternalURL)
	testGetMetadata(t, metadataAppExternalURL)
}
