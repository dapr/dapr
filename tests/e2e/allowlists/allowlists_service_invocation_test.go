// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package allowlists_service_invocation_e2e

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
	RemoteApp        string `json:"remoteApp,omitempty"`
	Method           string `json:"method,omitempty"`
	RemoteAppTracing string `json:"remoteAppTracing"`
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
			AppName:        "allowlists-caller",
			DaprEnabled:    true,
			ImageName:      "e2e-service_invocation",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
		},
		{
			Config:         "allowlistsappconfig",
			AppName:        "allowlists-callee-http",
			DaprEnabled:    true,
			ImageName:      "e2e-service_invocation",
			Replicas:       1,
			IngressEnabled: false,
			MetricsEnabled: true,
		},
		{
			Config:         "allowlistsgrpcappconfig",
			AppName:        "allowlists-callee-grpc",
			DaprEnabled:    true,
			ImageName:      "e2e-service_invocation_grpc",
			Replicas:       1,
			IngressEnabled: false,
			MetricsEnabled: true,
			AppProtocol:    "grpc",
		},
	}

	tr = runner.NewTestRunner("hellodapr", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

var allowListsForServiceInvocationTests = []struct {
	in                 string
	remoteApp          string
	appMethod          string
	expectedResponse   string
	calleeSide         string
	expectedStatusCode int
}{
	{
		"Test allow with callee side http",
		"allowlists-callee-http",
		"opAllow",
		"opAllow is called",
		"http",
		200,
	},
	{
		"Test deny with callee side http",
		"allowlists-callee-http",
		"opDeny",
		"fail to invoke, id: allowlists-callee-http, err: rpc error: code = PermissionDenied desc = access control policy has denied access to appid: allowlists-caller operation: opDeny verb: POST",
		"http",
		403,
	},
	{
		"Test allow with callee side grpc",
		"allowlists-callee-grpc",
		"grpctogrpctest",
		"success",
		"grpc",
		200,
	},
	{
		"Test deny with callee side grpc",
		"allowlists-callee-grpc",
		"httptogrpctest",
		"HTTP call failed with fail to invoke, id: allowlists-callee-grpc, err: rpc error: code = PermissionDenied desc = access control policy has denied access to appid: allowlists-caller operation: httpToGrpcTest verb: NONE",
		"grpc",
		403,
	},
}

func TestServiceInvocationWithAllowLists(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL("allowlists-caller")
	require.NotEmpty(t, externalURL, "external URL must not be empty!")
	var err error
	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err = utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	t.Logf("externalURL is '%s'\n", externalURL)

	for _, tt := range allowListsForServiceInvocationTests {
		t.Run(tt.in, func(t *testing.T) {
			body, err := json.Marshal(testCommandRequest{
				RemoteApp: tt.remoteApp,
				Method:    tt.appMethod,
			})
			require.NoError(t, err)

			var url string
			if tt.calleeSide == "http" {
				url = fmt.Sprintf("%s/tests/invoke_test", externalURL)
			} else {
				url = fmt.Sprintf("http://%s/%s", externalURL, tt.appMethod)
			}
			resp, statusCode, err := utils.HTTPPostWithStatus(
				url,
				body)
			t.Log("checking err...")
			require.NoError(t, err)

			var appResp appResponse
			t.Logf("unmarshalling..%s\n", string(resp))
			err = json.Unmarshal(resp, &appResp)
			require.NoError(t, err)
			require.Equal(t, tt.expectedResponse, appResp.Message)
			require.Equal(t, tt.expectedStatusCode, statusCode)
		})
	}
}
