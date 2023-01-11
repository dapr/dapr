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

package componenthealth_e2e

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
	appName         = "componenthealthapp" // App name in Dapr.
)

type mockHealthResponse struct {
	Component string `json:"componentName"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	ErrorCode string `json:"errorCode,omitempty"`
	Message   string `json:"message,omitempty"`
}

type mockAllHealthResponse struct {
	Result []mockHealthResponse `json:"result"`
}

var testCases = []struct {
	testName         string
	urlSuffix        string
	compHealthResult []componentStatus
}{
	{
		"all components",
		"GetComponentHealthAlpha1",
		expectedAllComponentsHealthResult(),
	},
}

type componentStatus struct {
	componentName string
	componentType string
	errorExpected bool
	status        string
	errorString   string
}

func expectedAllComponentsHealthResult() []componentStatus {
	return []componentStatus{
		{
			"querystatestore",
			"state",
			false,
			"OK",
			"", // no error
		},
		{
			"dapr-resiliency-pubsub",
			"pubsub",
			false,
			"OK",
			"", // no error
		},
		{
			"inmemorystate",
			"state",
			true,
			"UNDEFINED",
			"ERR_PING_NOT_IMPLEMENTED",
		},
	}
}

func testGetComponentHealth(t *testing.T, compHealthAppExternalURL string) {

	for _, tc := range testCases {
		t.Log("Getting health for components")
		url := fmt.Sprintf("%s/test/%s", compHealthAppExternalURL, tc.urlSuffix)
		resp, err := utils.HTTPGet(url)
		require.NoError(t, err)
		require.NotEmpty(t, resp, "response must not be empty!")
		var healthResponse mockAllHealthResponse
		err = json.Unmarshal(resp, &healthResponse)
		require.NoError(t, err)
		for _, expectedComponentResult := range tc.compHealthResult {
			for _, health := range healthResponse.Result {
				if expectedComponentResult.componentName == health.Component {
					require.Equal(t, expectedComponentResult.componentType, health.Type, "Expected Type to be equal")
					if !expectedComponentResult.errorExpected {
						require.Equal(t, expectedComponentResult.status, health.Status, "Expected Status to be equal")
					} else {
						require.Contains(t, string(health.ErrorCode), expectedComponentResult.errorString, "Expected error string to be contained")
					}
				}
			}
		}
	}
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
			ImageName:      "e2e-componenthealth",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
		},
	}

	log.Printf("Creating TestRunner\n")
	tr = runner.NewTestRunner("componenthealthtest", testApps, nil, nil)
	log.Printf("Starting TestRunner\n")
	os.Exit(tr.Start(m))
}

func TestComponentHealthApp(t *testing.T) {
	t.Log("Enter TestComponentHealthHTTP")
	compHealthAppExternalURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, compHealthAppExternalURL, "compHealthAppExternalURL must not be empty!")

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(compHealthAppExternalURL, numHealthChecks)
	require.NoError(t, err)
	testGetComponentHealth(t, compHealthAppExternalURL)
}
