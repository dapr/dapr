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
	"strings"
	"testing"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/stretchr/testify/assert"
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
		{
			AppName:        "grpcapp",
			DaprEnabled:    true,
			ImageName:      "e2e-service_invocation_grpc",
			Replicas:       1,
			IngressEnabled: false,
			AppProtocol:    "grpc",
		},
	}

	tr = runner.NewTestRunner("hellodapr", testApps, nil)
	os.Exit(tr.Start(m))
}

var serviceinvocationTests = []struct {
	in               string
	remoteApp        string
	appMethod        string
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

var moreServiceinvocationTests = []struct {
	in               string
	remoteApp        string
	appMethod        string
	expectedResponse string
}{
	// For descriptions, see corresponding methods in dapr/tests/apps/service_invocation/app.go
	{
		"Test HTTP to HTTP",
		"serviceinvocation-callee-1",
		"httptohttptest",
		"success",
	},
	{
		"Test HTTP to gRPC",
		"grpcapp",
		"httptogrpctest",
		"success",
	},
	{
		"Test gRPC to HTTP",
		"serviceinvocation-callee-1",
		"grpctohttptest",
		"success",
	},
	{
		"Test gRPC to gRPC",
		"grpcapp",
		"grpctogrpctest",
		"success",
	},
}

func TestServiceInvocation(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL("serviceinvocation-caller")
	require.NotEmpty(t, externalURL, "external URL must not be empty!")
	var err error
	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err = utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	t.Logf("externalURL is '%s'\n", externalURL)

	for _, tt := range serviceinvocationTests {
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

	for _, tt := range moreServiceinvocationTests {
		t.Run(tt.in, func(t *testing.T) {
			body, err := json.Marshal(testCommandRequest{
				RemoteApp: tt.remoteApp,
				Method:    tt.appMethod,
			})
			require.NoError(t, err)

			url := fmt.Sprintf("http://%s/%s", externalURL, tt.appMethod)

			t.Logf("url is '%s'\n", url)
			resp, err := utils.HTTPPost(
				url,
				body)

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

func TestHeaders(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL("serviceinvocation-caller")
	require.NotEmpty(t, externalURL, "external URL must not be empty!")
	var err error
	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err = utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	t.Logf("externalURL is '%s'\n", externalURL)

	t.Run("http-to-http", func(t *testing.T) {
		body, err := json.Marshal(testCommandRequest{
			RemoteApp: "serviceinvocation-callee-0",
			Method:    "http-to-http",
		})
		require.NoError(t, err)

		resp, err := utils.HTTPPost(
			fmt.Sprintf("http://%s/tests/v1_httptohttptest", externalURL), body)
		t.Log("checking err...")
		require.NoError(t, err)

		var appResp appResponse
		t.Logf("unmarshalling..%s\n", string(resp))
		err = json.Unmarshal(resp, &appResp)

		var actualHeaders = map[string]string{}
		json.Unmarshal([]byte(appResp.Message), &actualHeaders)
		var requestHeaders = map[string][]string{}
		var responseHeaders = map[string][]string{}
		json.Unmarshal([]byte(actualHeaders["request"]), &requestHeaders)
		json.Unmarshal([]byte(actualHeaders["response"]), &responseHeaders)

		require.NoError(t, err)
		assert.NotNil(t, requestHeaders["Accept-Encoding"][0])
		assert.NotNil(t, requestHeaders["Content-Length"][0])
		assert.Equal(t, "application/json", requestHeaders["Content-Type"][0])
		assert.Equal(t, "DaprValue1", requestHeaders["Daprtest-Request-1"][0])
		assert.Equal(t, "DaprValue2", requestHeaders["Daprtest-Request-2"][0])
		assert.NotNil(t, requestHeaders["Forwarded"][0])
		assert.NotNil(t, requestHeaders["Traceparent"][0])
		assert.NotNil(t, requestHeaders["User-Agent"][0])
		assert.NotNil(t, requestHeaders["X-Forwarded-For"][0])
		assert.Equal(t, "http", requestHeaders["X-Forwarded-Proto"][0])

		assert.NotNil(t, responseHeaders["Content-Length"][0])
		assert.Equal(t, "application/json; utf-8", responseHeaders["Content-Type"][0])
		assert.Equal(t, "DaprTest-Response-Value-1", responseHeaders["Daprtest-Response-1"][0])
		assert.Equal(t, "DaprTest-Response-Value-2", responseHeaders["Daprtest-Response-2"][0])
	})

	t.Run("grpc-to-grpc", func(t *testing.T) {
		body, err := json.Marshal(testCommandRequest{
			RemoteApp: "grpcapp",
			Method:    "grpc-to-grpc",
		})
		require.NoError(t, err)

		resp, err := utils.HTTPPost(
			fmt.Sprintf("http://%s/tests/v1_grpctogrpctest", externalURL), body)
		t.Log("checking err...")
		require.NoError(t, err)

		var appResp appResponse
		t.Logf("unmarshalling..%s\n", string(resp))
		err = json.Unmarshal(resp, &appResp)

		var actualHeaders = map[string]string{}
		json.Unmarshal([]byte(appResp.Message), &actualHeaders)
		var requestHeaders = map[string][]string{}
		var responseHeaders = map[string][]string{}
		var trailerHeaders = map[string][]string{}
		json.Unmarshal([]byte(actualHeaders["request"]), &requestHeaders)
		json.Unmarshal([]byte(actualHeaders["response"]), &responseHeaders)
		json.Unmarshal([]byte(actualHeaders["trailers"]), &trailerHeaders)

		require.NoError(t, err)
		assert.Equal(t, "application/grpc", requestHeaders["content-type"][0])
		assert.Equal(t, "127.0.0.1:3000", requestHeaders[":authority"][0])
		assert.Equal(t, "DaprValue1", requestHeaders["daprtest-request-1"][0])
		assert.Equal(t, "DaprValue2", requestHeaders["daprtest-request-2"][0])
		assert.NotNil(t, requestHeaders["user-agent"][0])
		assert.NotNil(t, requestHeaders["grpc-trace-bin"][0])
		// TODO: When we fix double grpc-trace-bin
		// assert.Equal(t, 1, len(requestHeaders["grpc-trace-bin"]))

		assert.Equal(t, "application/grpc", responseHeaders["content-type"][0])
		assert.Equal(t, "DaprTest-Response-Value-1", responseHeaders["daprtest-response-1"][0])
		assert.Equal(t, "DaprTest-Response-Value-2", responseHeaders["daprtest-response-2"][0])

		assert.Equal(t, "DaprTest-Trailer-Value-1", trailerHeaders["daprtest-trailer-1"][0])
		assert.Equal(t, "DaprTest-Trailer-Value-2", trailerHeaders["daprtest-trailer-2"][0])
	})

	t.Run("grpc-to-http", func(t *testing.T) {
		body, err := json.Marshal(testCommandRequest{
			RemoteApp: "serviceinvocation-callee-0",
			Method:    "grpc-to-grpc",
		})
		require.NoError(t, err)

		resp, err := utils.HTTPPost(
			fmt.Sprintf("http://%s/tests/v1_grpctohttptest", externalURL), body)
		t.Log("checking err...")
		require.NoError(t, err)

		var appResp appResponse
		t.Logf("unmarshalling..%s\n", string(resp))
		err = json.Unmarshal(resp, &appResp)

		var actualHeaders = map[string]string{}
		json.Unmarshal([]byte(appResp.Message), &actualHeaders)
		var requestHeaders = map[string][]string{}
		var responseHeaders = map[string][]string{}
		json.Unmarshal([]byte(actualHeaders["request"]), &requestHeaders)
		json.Unmarshal([]byte(actualHeaders["response"]), &responseHeaders)

		require.NoError(t, err)
		assert.NotNil(t, requestHeaders["Content-Length"][0])
		assert.Equal(t, "text/plain; utf-8", requestHeaders["Content-Type"][0])
		assert.Equal(t, "localhost:50001", requestHeaders["Dapr-Authority"][0])
		assert.Equal(t, "DaprValue1", requestHeaders["Daprtest-Request-1"][0])
		assert.Equal(t, "DaprValue2", requestHeaders["Daprtest-Request-2"][0])
		assert.NotNil(t, requestHeaders["Traceparent"][0])
		assert.NotNil(t, requestHeaders["User-Agent"][0])

		assert.NotNil(t, responseHeaders["content-length"][0])
		assert.Equal(t, "application/grpc", responseHeaders["content-type"][0])
		assert.Equal(t, "application/json; utf-8", responseHeaders["dapr-content-type"][0])
		assert.NotNil(t, responseHeaders["dapr-date"][0])
		assert.Equal(t, "DaprTest-Response-Value-1", responseHeaders["daprtest-response-1"][0])
		assert.Equal(t, "DaprTest-Response-Value-2", responseHeaders["daprtest-response-2"][0])
	})

	t.Run("http-to-grpc", func(t *testing.T) {
		body, err := json.Marshal(testCommandRequest{
			RemoteApp: "grpcapp",
			Method:    "http-to-grpc",
		})
		require.NoError(t, err)

		resp, err := utils.HTTPPost(
			fmt.Sprintf("http://%s/tests/v1_httptogrpctest", externalURL), body)
		t.Log("checking err...")
		require.NoError(t, err)

		var appResp appResponse
		t.Logf("unmarshalling..%s\n", string(resp))
		err = json.Unmarshal(resp, &appResp)

		var actualHeaders = map[string]string{}
		json.Unmarshal([]byte(appResp.Message), &actualHeaders)
		var requestHeaders = map[string][]string{}
		var responseHeaders = map[string][]string{}
		json.Unmarshal([]byte(actualHeaders["request"]), &requestHeaders)
		json.Unmarshal([]byte(actualHeaders["response"]), &responseHeaders)

		require.NoError(t, err)

		assert.True(t, strings.HasPrefix(requestHeaders["dapr-host"][0], "localhost:"))
		assert.Equal(t, "application/grpc", requestHeaders["content-type"][0])
		assert.True(t, strings.HasPrefix(requestHeaders[":authority"][0], "127.0.0.1:"))
		assert.Equal(t, "DaprValue1", requestHeaders["daprtest-request-1"][0])
		assert.Equal(t, "DaprValue2", requestHeaders["daprtest-request-2"][0])
		assert.NotNil(t, requestHeaders["user-agent"][0])
		assert.NotNil(t, requestHeaders["grpc-trace-bin"][0])
		// TODO: When we fix double grpc-trace-bin
		// assert.Equal(t, 1, len(requestHeaders["grpc-trace-bin"]))
		assert.NotNil(t, requestHeaders["x-forwarded-host"][0])
		assert.Equal(t, "http", requestHeaders["x-forwarded-proto"][0])
		assert.NotNil(t, requestHeaders["forwarded"][0])
		assert.NotNil(t, requestHeaders["x-forwarded-for"][0])

		assert.NotNil(t, responseHeaders["Content-Length"][0])
		assert.Equal(t, "application/json", responseHeaders["Content-Type"][0])
		assert.NotNil(t, responseHeaders["Date"][0])
		assert.Equal(t, "DaprTest-Response-Value-1", responseHeaders["Daprtest-Response-1"][0])
		assert.Equal(t, "DaprTest-Response-Value-2", responseHeaders["Daprtest-Response-2"][0])
	})

}
