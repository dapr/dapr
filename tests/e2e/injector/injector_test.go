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

package injector_e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/stretchr/testify/require"
	apiv1 "k8s.io/api/core/v1"
)

const (
	appName         = "injectorapp"
	numHealthChecks = 60 // Number of get calls before starting tests.
)

// requestResponse represents a request or response for the APIs in the app.
type requestResponse struct {
	Message   string `json:"message,omitempty"`
	StartTime int    `json:"start_time,omitempty"`
	EndTime   int    `json:"end_time,omitempty"`
}

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	utils.SetupLogs(appName)
	utils.InitHTTPClient(true)

	// These apps will be deployed for injector test before starting actual test
	// and will be cleaned up after all tests are finished automatically
	comps := []kube.ComponentDescription{
		{
			Name:     "local-secret-store",
			TypeName: "secretstores.local.file",
			MetaData: map[string]kube.MetadataValue{
				"secretsFile": {Raw: `"/tmp/testdata/secrets.json"`},
			},
			Scopes: []string{appName},
		},
		{
			Name:     "secured-binding",
			TypeName: "bindings.http",
			MetaData: map[string]kube.MetadataValue{
				"url": {Raw: `"https://localhost:3001"`},
			},
			Scopes: []string{appName},
		},
	}

	testApps := []kube.AppDescription{
		{
			AppName:           appName,
			DaprEnabled:       true,
			ImageName:         fmt.Sprintf("e2e-%s", appName),
			Replicas:          1,
			IngressEnabled:    true,
			MetricsEnabled:    true,
			DaprMemoryLimit:   "200Mi",
			DaprMemoryRequest: "100Mi",
			AppMemoryLimit:    "200Mi",
			AppMemoryRequest:  "100Mi",
			DaprVolumeMounts:  "storage-volume:/tmp/testdata/",
			DaprEnv:           "SSL_CERT_DIR=/tmp/testdata/certs",
			Volumes: []apiv1.Volume{
				{
					Name: "storage-volume",
					VolumeSource: apiv1.VolumeSource{
						EmptyDir: &apiv1.EmptyDirVolumeSource{},
					},
				},
			},
			AppVolumeMounts: []apiv1.VolumeMount{
				{
					Name:      "storage-volume",
					MountPath: "/tmp/testdata/",
					ReadOnly:  true,
				},
			},
			InitContainers: []apiv1.Container{
				{
					Name:            fmt.Sprintf("%s-init", appName),
					Image:           runner.BuildTestImageName(fmt.Sprintf("e2e-%s-init", appName)),
					ImagePullPolicy: apiv1.PullAlways,
					VolumeMounts: []apiv1.VolumeMount{
						{
							Name:      "storage-volume",
							MountPath: "/tmp/testdata",
						},
					},
				},
			},
		},
	}

	tr = runner.NewTestRunner(appName, testApps, comps, nil)
	os.Exit(tr.Start(m))
}

func TestDaprVolumeMount(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, externalURL, "external URL must not be empty!")

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	// setup
	url := fmt.Sprintf("%s/tests/testVolumeMount", externalURL)

	// act
	resp, statusCode, err := utils.HTTPPostWithStatus(url, []byte{})

	// assert
	require.NoError(t, err)

	var appResp requestResponse
	err = json.Unmarshal(resp, &appResp)
	require.NoError(t, err)

	require.Equal(t, 200, statusCode)
	require.Equal(t, "secret-value", appResp.Message)
}

func TestDaprSslCertInstallation(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, externalURL, "external URL must not be empty!")

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	// setup
	url := fmt.Sprintf("%s/tests/testBinding", externalURL)

	// act
	_, statusCode, err := utils.HTTPPostWithStatus(url, []byte{})

	// assert
	require.NoError(t, err)

	require.Equal(t, 200, statusCode)
}
