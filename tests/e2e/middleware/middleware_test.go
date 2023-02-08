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

package middleware_e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	apiv1 "k8s.io/api/core/v1"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
)

const numHealthChecks = 60 // Number of get calls before starting tests.

type testResponse struct {
	Input  string `json:"input"`
	Output string `json:"output"`
}

var tr *runner.TestRunner

func getExternalURL(t *testing.T, appName string) string {
	externalURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, externalURL, "external URL must not be empty!")
	return externalURL
}

func healthCheckApp(t *testing.T, externalURL string, numHealthChecks int) {
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)
}

func TestMain(m *testing.M) {
	utils.SetupLogs("middleware")
	utils.InitHTTPClient(true)

	// These apps have a Configuration object passed as --config-file and loaded from a ConfigMap resource
	// Likewise, Components are passed using --resources-path and loaded from a ConfigMap resource
	componentsVolume := apiv1.Volume{
		Name: "components",
		VolumeSource: apiv1.VolumeSource{
			ConfigMap: &apiv1.ConfigMapVolumeSource{
				LocalObjectReference: apiv1.LocalObjectReference{
					Name: "components-cm",
				},
			},
		},
	}
	testApps := []kube.AppDescription{
		{
			AppName:          "middlewareapp",
			DaprEnabled:      true,
			ImageName:        "e2e-middleware",
			Replicas:         1,
			IngressEnabled:   true,
			MetricsEnabled:   true,
			DaprVolumeMounts: "components:/mnt/components,config:/mnt/config",
			ConfigFile:       "/mnt/config/pipeline.yaml",
			ResourcesPath:    "/mnt/components",
			Volumes: []apiv1.Volume{
				componentsVolume,
				{
					Name: "config",
					VolumeSource: apiv1.VolumeSource{
						ConfigMap: &apiv1.ConfigMapVolumeSource{
							LocalObjectReference: apiv1.LocalObjectReference{
								Name: "pipeline-cm",
							},
							Items: []apiv1.KeyToPath{
								{Key: "pipeline.yaml", Path: "pipeline.yaml"},
							},
						},
					},
				},
			},
		},
		{
			AppName:          "app-channel-middleware",
			DaprEnabled:      true,
			ImageName:        "e2e-middleware",
			Replicas:         1,
			IngressEnabled:   true,
			MetricsEnabled:   true,
			DaprVolumeMounts: "components:/mnt/components,config:/mnt/config",
			ConfigFile:       "/mnt/config/app-channel-pipeline.yaml",
			ResourcesPath:    "/mnt/components",
			Volumes: []apiv1.Volume{
				componentsVolume,
				{
					Name: "config",
					VolumeSource: apiv1.VolumeSource{
						ConfigMap: &apiv1.ConfigMapVolumeSource{
							LocalObjectReference: apiv1.LocalObjectReference{
								Name: "pipeline-cm",
							},
							Items: []apiv1.KeyToPath{
								{Key: "app-channel-pipeline.yaml", Path: "app-channel-pipeline.yaml"},
							},
						},
					},
				},
			},
		},
		{
			AppName:        "no-middleware",
			DaprEnabled:    true,
			ImageName:      "e2e-middleware",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
		},
	}

	tr = runner.NewTestRunner("middleware", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestSimpleMiddleware(t *testing.T) {
	middlewareURL := getExternalURL(t, "middlewareapp")
	appMiddlewareURL := getExternalURL(t, "app-channel-middleware")
	noMiddlewareURL := getExternalURL(t, "no-middleware")

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	healthCheckApp(t, middlewareURL, numHealthChecks)
	healthCheckApp(t, noMiddlewareURL, numHealthChecks)

	t.Logf("middlewareURL is '%s'\n", middlewareURL)
	t.Logf("noMiddlewareURL is '%s'\n", noMiddlewareURL)

	t.Run("test_basicMiddleware", func(t *testing.T) {
		resp, status, err := utils.HTTPPostWithStatus(fmt.Sprintf("http://%s/test/logCall/%s", middlewareURL, "middlewareapp"), []byte{})

		require.Nil(t, err)
		require.Equal(t, 200, status)
		require.NotNil(t, resp)

		var results testResponse
		json.Unmarshal(resp, &results)

		require.Equal(t, "hello", results.Input)
		require.Equal(t, "HELLO", results.Output)
	})

	t.Run("test_basicAppChannelMiddleware", func(t *testing.T) {
		resp, status, err := utils.HTTPPostWithStatus(fmt.Sprintf("http://%s/test/logCall/%s", appMiddlewareURL, "app-channel-middleware"), []byte{})

		require.Nil(t, err)
		require.Equal(t, 200, status)
		require.NotNil(t, resp)

		var results testResponse
		json.Unmarshal(resp, &results)

		require.Equal(t, "hello", results.Input)
		require.Equal(t, "HELLO", results.Output)
	})

	t.Run("test_noMiddleware", func(t *testing.T) {
		resp, status, err := utils.HTTPPostWithStatus(fmt.Sprintf("http://%s/test/logCall/%s", noMiddlewareURL, "no-middleware"), []byte{})

		require.Nil(t, err)
		require.Equal(t, 200, status)
		require.NotNil(t, resp)

		var results testResponse
		json.Unmarshal(resp, &results)

		require.Equal(t, "hello", results.Input)
		require.Equal(t, "hello", results.Output)
	})
}
