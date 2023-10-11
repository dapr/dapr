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
	utils.SetupLogs("allowlists")
	utils.InitHTTPClient(false)

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
		{
			AppName:        "grpcproxyclient",
			DaprEnabled:    true,
			ImageName:      "e2e-service_invocation_grpc_proxy_client",
			Replicas:       1,
			AppProtocol:    "grpc",
			IngressEnabled: true,
			MetricsEnabled: true,
		},
		{
			Config:         "allowlistsgrpcappconfig",
			AppName:        "grpcproxyserver",
			DaprEnabled:    true,
			ImageName:      "e2e-service_invocation_grpc_proxy_server",
			Replicas:       1,
			IngressEnabled: false,
			MetricsEnabled: true,
			AppProtocol:    "grpc",
			AppPort:        50051,
		},
	}

	tr = runner.NewTestRunner("allowlists", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

var allowListsForServiceInvocationTests = []struct {
	in                 string
	path               string
	remoteApp          string
	appMethod          string
	expectedResponse   string
	calleeSide         string
	expectedStatusCode int
}{
	{
		"Test allow with callee side http",
		"opAllow",
		"allowlists-callee-http",
		"opAllow",
		"opAllow is called",
		"http",
		200,
	},
	{
		"Test deny with callee side http",
		"opDeny",
		"allowlists-callee-http",
		"opDeny",
		"failed to invoke, id: allowlists-callee-http, err: rpc error: code = PermissionDenied desc = access control policy has denied access to id: spiffe://public/ns/dapr-tests/allowlists-caller operation: opDeny verb: POST",
		"http",
		403,
	},
	{
		"Test allow with callee side grpc",
		"grpctogrpctest",
		"allowlists-callee-grpc",
		"grpcToGrpcTest",
		"success",
		"grpc",
		200,
	},
	{
		"Test allow with callee side grpc without http verb",
		"grpctogrpctest",
		"allowlists-callee-grpc",
		"grpcToGrpcWithoutVerbTest",
		"success",
		"grpc",
		200,
	},
	{
		"Test deny with callee side grpc",
		"httptogrpctest",
		"allowlists-callee-grpc",
		"httptogrpctest",
		"HTTP call failed with failed to invoke, id: allowlists-callee-grpc, err: rpc error: code = PermissionDenied desc = access control policy has denied access to id: spiffe://public/ns/dapr-tests/allowlists-caller operation: httpToGrpcTest verb: NONE",
		"grpc",
		403,
	},
}

var allowListsForServiceInvocationForProxyTests = []struct {
	in                 string
	path               string
	remoteApp          string
	appMethod          string
	expectedResponse   string
	calleeSide         string
	expectedStatusCode int
}{
	{
		"Test allow with callee side grpc without http verb by grpc proxy",
		"",
		"grpcproxyclient",
		"",
		"success",
		"grpc",
		200,
	},
}

func TestServiceInvocationWithAllowListsForGrpcProxy(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL("grpcproxyclient")
	require.NotEmpty(t, externalURL, "external URL must not be empty!")
	var err error
	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err = utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	t.Logf("externalURL is '%s'\n", externalURL)

	for _, tt := range allowListsForServiceInvocationForProxyTests {
		t.Run(tt.in, func(t *testing.T) {
			body, err := json.Marshal(testCommandRequest{
				RemoteApp: tt.remoteApp,
				Method:    tt.appMethod,
			})
			require.NoError(t, err)

			resp, err := utils.HTTPPost(
				fmt.Sprintf("%s/tests/invoke_test", externalURL), body)
			t.Log("checking err...")
			require.NoError(t, err)

			var appResp appResponse
			t.Logf("unmarshalling..%s\n", string(resp))
			err = json.Unmarshal(resp, &appResp)
			require.NoError(t, err)
			require.Equal(t, tt.expectedResponse, appResp.Message)
		})
	}
}

func TestServiceInvocationWithAllowLists(t *testing.T) {
	config, err := tr.Platform.GetConfiguration("daprsystem")
	if err != nil {
		t.Logf("configuration name: daprsystem, get failed: %s \n", err.Error())
		os.Exit(-1)
	}
	if !config.Spec.MTLSSpec.GetEnabled() {
		t.Logf("mtls disabled. can't running unit tests")
		return
	}
	externalURL := tr.Platform.AcquireAppExternalURL("allowlists-caller")
	require.NotEmpty(t, externalURL, "external URL must not be empty!")
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
				url = fmt.Sprintf("http://%s/%s", externalURL, tt.path)
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
