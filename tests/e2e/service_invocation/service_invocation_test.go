// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package service_invocation_e2e

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

type testCommandRequest struct {
	RemoteApp string `json:"remoteApp,omitempty"`
	Method    string `json:"method,omitempty"`
}

type appResponse struct {
	Message string `json:"message,omitempty"`
}

const numHealthChecks = 60 // Number of times to call the endpoint to check for health.

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	// These apps will be deployed for hellodapr test before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:        "serviceinvocation-caller",
			DaprEnabled:    true,
			ImageName:      "e2e-service_invocation",
			Replicas:       1,
			IngressEnabled: true,
		},
		{
			AppName:        "serviceinvocation-callee-0",
			DaprEnabled:    true,
			ImageName:      "e2e-service_invocation",
			Replicas:       1,
			IngressEnabled: false,
		},
		{
			AppName:        "serviceinvocation-callee-1",
			DaprEnabled:    true,
			ImageName:      "e2e-service_invocation",
			Replicas:       1,
			IngressEnabled: false,
		},
	}

	tr = runner.NewTestRunner("hellodapr", testApps, nil)
	os.Exit(tr.Start(m))
}

var serviceinvocationTests = []struct {
	in               string
	remoteApp        string
	method           string
	expectedResponse string
}{
	{
		"Test singlehop for callee-0",
		"serviceinvocation-callee-0",
		"singlehop",
		"singlehop is called",
	},
	{
		"Test singlehop for callee-1",
		"serviceinvocation-callee-1",
		"singlehop",
		"singlehop is called",
	},
	{
		"Test multihop",
		"serviceinvocation-callee-0",
		"multihop",
		"singlehop is called",
	},
}

func TestServiceInvocation(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL("serviceinvocation-caller")
	require.NotEmpty(t, externalURL, "external URL must not be empty!")

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	for _, tt := range serviceinvocationTests {
		t.Run(tt.in, func(t *testing.T) {
			body, err := json.Marshal(testCommandRequest{
				RemoteApp: tt.remoteApp,
				Method:    tt.method,
			})
			require.NoError(t, err)

			resp, err := utils.HTTPPost(fmt.Sprintf("%s/tests/invoke_test", externalURL), body)
			require.NoError(t, err)

			var appResp appResponse
			err = json.Unmarshal(resp, &appResp)
			require.NoError(t, err)
			require.Equal(t, tt.expectedResponse, appResp.Message)
		})
	}
}
