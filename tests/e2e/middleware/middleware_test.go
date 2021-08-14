// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package middleware_e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/stretchr/testify/require"
)

const (
	appName         = "middlewareapp" // App name in Dapr.
	numHealthChecks = 60              // Number of get calls before starting tests.
)

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
	// These apps will be deployed before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:        appName,
			DaprEnabled:    true,
			ImageName:      "e2e-middleware",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
			Config:         "pipeline",
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

	tr = runner.NewTestRunner(appName, testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestSimpleMiddleware(t *testing.T) {
	middlewareURL := getExternalURL(t, appName)
	noMiddlewareURL := getExternalURL(t, "no-middleware")

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	healthCheckApp(t, middlewareURL, numHealthChecks)
	healthCheckApp(t, noMiddlewareURL, numHealthChecks)

	t.Logf("middlewareURL is '%s'\n", middlewareURL)
	t.Logf("noMiddlewareURL is '%s'\n", noMiddlewareURL)

	t.Run("test_basic_middleware", func(t *testing.T) {
		resp, status, err := utils.HTTPPostWithStatus(fmt.Sprintf("http://%s/test/logCall/%s", middlewareURL, appName), []byte{})

		require.Nil(t, err)
		require.Equal(t, 200, status)
		require.NotNil(t, resp)

		var results testResponse
		json.Unmarshal(resp, &results)

		require.Equal(t, "hello", results.Input)
		require.Equal(t, "HELLO", results.Output)
	})

	t.Run("test_no_middleware", func(t *testing.T) {
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
