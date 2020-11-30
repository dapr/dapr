// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package service_invocation_e2e

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opencensus.io/trace/propagation"
)

type testCommandRequest struct {
	RemoteApp        string `json:"remoteApp,omitempty"`
	Method           string `json:"method,omitempty"`
	RemoteAppTracing string `json:"remoteAppTracing"`
}

type appResponse struct {
	Message string `json:"message,omitempty"`
}

type negativeTestResult struct {
	MainCallSuccessful bool                   `json:"callSuccessful"`
	RawBody            []byte                 `json:"rawBody"`
	RawError           string                 `json:"rawError"`
	Results            []individualTestResult `json:"results"`
}

type individualTestResult struct {
	TestCase       string `json:"case"`
	CallSuccessful bool   `json:"callSuccessful"`
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

	tr = runner.NewTestRunner("hellodapr", testApps, nil, nil)
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

	hostname, hostIP, err := tr.Platform.GetAppHostDetails("serviceinvocation-caller")
	require.NoError(t, err, "error retrieving host details: %s", err)

	expectedForwarded := fmt.Sprintf("for=%s;by=%s;host=%s", hostIP, hostIP, hostname)

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
		assert.NotNil(t, requestHeaders["Traceparent"][0])
		assert.NotNil(t, requestHeaders["User-Agent"][0])
		assert.Equal(t, hostIP, requestHeaders["X-Forwarded-For"][0])
		assert.Equal(t, hostname, requestHeaders["X-Forwarded-Host"][0])
		assert.Equal(t, expectedForwarded, requestHeaders["Forwarded"][0])

		assert.NotNil(t, responseHeaders["Content-Length"][0])
		assert.Equal(t, "application/json; utf-8", responseHeaders["Content-Type"][0])
		assert.Equal(t, "DaprTest-Response-Value-1", responseHeaders["Daprtest-Response-1"][0])
		assert.Equal(t, "DaprTest-Response-Value-2", responseHeaders["Daprtest-Response-2"][0])
		assert.NotNil(t, responseHeaders["Traceparent"][0])
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
		assert.Equal(t, 1, len(requestHeaders["grpc-trace-bin"]))
		assert.Equal(t, hostIP, requestHeaders["x-forwarded-for"][0])
		assert.Equal(t, hostname, requestHeaders["x-forwarded-host"][0])
		assert.Equal(t, expectedForwarded, requestHeaders["forwarded"][0])

		assert.Equal(t, "application/grpc", responseHeaders["content-type"][0])
		assert.Equal(t, "DaprTest-Response-Value-1", responseHeaders["daprtest-response-1"][0])
		assert.Equal(t, "DaprTest-Response-Value-2", responseHeaders["daprtest-response-2"][0])
		assert.NotNil(t, responseHeaders["grpc-trace-bin"][0])
		assert.Equal(t, 1, len(responseHeaders["grpc-trace-bin"]))

		assert.Equal(t, "DaprTest-Trailer-Value-1", trailerHeaders["daprtest-trailer-1"][0])
		assert.Equal(t, "DaprTest-Trailer-Value-2", trailerHeaders["daprtest-trailer-2"][0])
	})

	t.Run("grpc-to-http", func(t *testing.T) {
		body, err := json.Marshal(testCommandRequest{
			RemoteApp: "serviceinvocation-callee-0",
			Method:    "grpc-to-http",
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
		assert.Equal(t, hostIP, requestHeaders["X-Forwarded-For"][0])
		assert.Equal(t, hostname, requestHeaders["X-Forwarded-Host"][0])
		assert.Equal(t, expectedForwarded, requestHeaders["Forwarded"][0])

		assert.NotNil(t, responseHeaders["dapr-content-length"][0])
		assert.Equal(t, "application/grpc", responseHeaders["content-type"][0])
		assert.Equal(t, "application/json; utf-8", responseHeaders["dapr-content-type"][0])
		assert.NotNil(t, responseHeaders["dapr-date"][0])
		assert.Equal(t, "DaprTest-Response-Value-1", responseHeaders["daprtest-response-1"][0])
		assert.Equal(t, "DaprTest-Response-Value-2", responseHeaders["daprtest-response-2"][0])
		assert.NotNil(t, responseHeaders["grpc-trace-bin"][0])
		assert.Equal(t, 1, len(responseHeaders["grpc-trace-bin"]))
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

		assert.Nil(t, requestHeaders["connection"])
		assert.Nil(t, requestHeaders["content-length"])
		assert.True(t, strings.HasPrefix(requestHeaders["dapr-host"][0], "localhost:"))
		assert.Equal(t, "application/grpc", requestHeaders["content-type"][0])
		assert.True(t, strings.HasPrefix(requestHeaders[":authority"][0], "127.0.0.1:"))
		assert.Equal(t, "DaprValue1", requestHeaders["daprtest-request-1"][0])
		assert.Equal(t, "DaprValue2", requestHeaders["daprtest-request-2"][0])
		assert.NotNil(t, requestHeaders["user-agent"][0])
		assert.NotNil(t, requestHeaders["grpc-trace-bin"][0])
		assert.Equal(t, 1, len(requestHeaders["grpc-trace-bin"]))
		assert.Equal(t, hostIP, requestHeaders["x-forwarded-for"][0])
		assert.Equal(t, hostname, requestHeaders["x-forwarded-host"][0])
		assert.Equal(t, expectedForwarded, requestHeaders["forwarded"][0])

		assert.NotNil(t, responseHeaders["Content-Length"][0])
		assert.Equal(t, "application/json", responseHeaders["Content-Type"][0])
		assert.NotNil(t, responseHeaders["Date"][0])
		assert.Equal(t, "DaprTest-Response-Value-1", responseHeaders["Daprtest-Response-1"][0])
		assert.Equal(t, "DaprTest-Response-Value-2", responseHeaders["Daprtest-Response-2"][0])
		assert.NotNil(t, responseHeaders["Traceparent"][0])
	})

	/* Tracing specific tests */
	/*
		// following is the span context of expectedTraceID
		trace.SpanContext{
		TraceID:      trace.TraceID{75, 249, 47, 53, 119, 179, 77, 166, 163, 206, 146, 157, 14, 14, 71, 54},
		SpanID:       trace.SpanID{0, 240, 103, 170, 11, 169, 2, 183},
		TraceOptions: trace.TraceOptions(1),
		}

		string representation of span context : "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

		all the -bin headers are stored in Dapr as base64 encoded string.
		for the above span context when passed in grpc-trace-bin header, Dapr retrieved binary header and stored as encoded string.
		the encoded string for the above span context is :
		"AABL+S81d7NNpqPOkp0ODkc2AQDwZ6oLqQK3AgE="
	*/
	expectedTraceID := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	expectedEncodedTraceID := "AABL+S81d7NNpqPOkp0ODkc2AQDwZ6oLqQK3AgE="

	t.Run("http-to-http-tracing", func(t *testing.T) {
		body, err := json.Marshal(testCommandRequest{
			RemoteApp:        "serviceinvocation-callee-0",
			Method:           "http-to-http-tracing",
			RemoteAppTracing: "true",
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

		assert.NotNil(t, requestHeaders["Traceparent"][0])
		assert.Equal(t, expectedTraceID, requestHeaders["Daprtest-Traceid"][0])

		assert.NotNil(t, responseHeaders["Traceparent"][0])
		assert.Equal(t, expectedTraceID, responseHeaders["Traceparent"][0])
	})

	t.Run("grpc-to-grpc-tracing", func(t *testing.T) {
		body, err := json.Marshal(testCommandRequest{
			RemoteApp:        "grpcapp",
			Method:           "grpc-to-grpc-tracing",
			RemoteAppTracing: "true",
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

		assert.NotNil(t, requestHeaders["grpc-trace-bin"][0])
		assert.Equal(t, 1, len(requestHeaders["grpc-trace-bin"]))

		assert.NotNil(t, responseHeaders["grpc-trace-bin"][0])
		assert.Equal(t, 1, len(responseHeaders["grpc-trace-bin"]))
		traceContext := responseHeaders["grpc-trace-bin"][0]

		t.Logf("received response grpc header..%s\n", traceContext)
		assert.Equal(t, expectedEncodedTraceID, traceContext)
		decoded, _ := base64.StdEncoding.DecodeString(traceContext)
		gotSc, ok := propagation.FromBinary([]byte(decoded))

		assert.True(t, ok)
		assert.NotNil(t, gotSc)
		assert.Equal(t, expectedTraceID, diag.SpanContextToW3CString(gotSc))
	})

	t.Run("http-to-grpc-tracing", func(t *testing.T) {
		body, err := json.Marshal(testCommandRequest{
			RemoteApp:        "grpcapp",
			Method:           "http-to-grpc-tracing",
			RemoteAppTracing: "true",
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

		assert.NotNil(t, requestHeaders["grpc-trace-bin"][0])
		assert.Equal(t, 1, len(requestHeaders["grpc-trace-bin"]))

		assert.NotNil(t, responseHeaders["Traceparent"][0])
		assert.Equal(t, expectedTraceID, responseHeaders["Traceparent"][0])
	})

	t.Run("grpc-to-http-tracing", func(t *testing.T) {
		body, err := json.Marshal(testCommandRequest{
			RemoteApp:        "serviceinvocation-callee-0",
			Method:           "grpc-to-http-tracing",
			RemoteAppTracing: "true",
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

		assert.NotNil(t, requestHeaders["Traceparent"][0])
		assert.Equal(t, expectedTraceID, requestHeaders["Daprtest-Traceid"][0])

		assert.NotNil(t, responseHeaders["grpc-trace-bin"][0])
		assert.Equal(t, 1, len(responseHeaders["grpc-trace-bin"]))
		traceContext := responseHeaders["grpc-trace-bin"][0]

		t.Logf("received response grpc header..%s\n", traceContext)
		assert.Equal(t, expectedEncodedTraceID, traceContext)
		decoded, _ := base64.StdEncoding.DecodeString(traceContext)
		gotSc, ok := propagation.FromBinary([]byte(decoded))

		assert.True(t, ok)
		assert.NotNil(t, gotSc)
		assert.Equal(t, expectedTraceID, diag.SpanContextToW3CString(gotSc))
	})
}

func TestNegativeCases(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL("serviceinvocation-caller")
	require.NotEmpty(t, externalURL, "external URL must not be empty!")

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	t.Logf("externalURL is '%s'\n", externalURL)

	t.Run("missing_method_http", func(t *testing.T) {
		body, err := json.Marshal(testCommandRequest{
			RemoteApp:        "serviceinvocation-callee-0",
			Method:           "missing",
			RemoteAppTracing: "true",
		})
		require.NoError(t, err)

		resp, status, err := utils.HTTPPostWithStatus(fmt.Sprintf("http://%s/badservicecalltesthttp", externalURL), body)

		var testResults negativeTestResult
		json.Unmarshal(resp, &testResults)

		// TODO: This doesn't return as an error, it should be handled more gracefully in dapr
		require.False(t, testResults.MainCallSuccessful)
		require.Equal(t, 404, status)
		require.Contains(t, string(testResults.RawBody), "404 page not found")
		require.Nil(t, err)
	})

	t.Run("missing_method_grpc", func(t *testing.T) {
		body, err := json.Marshal(testCommandRequest{
			RemoteApp:        "serviceinvocation-callee-0",
			Method:           "missing",
			RemoteAppTracing: "true",
		})
		require.NoError(t, err)

		resp, status, err := utils.HTTPPostWithStatus(fmt.Sprintf("http://%s/badservicecalltestgrpc", externalURL), body)

		var testResults negativeTestResult
		json.Unmarshal(resp, &testResults)

		// TODO: This doesn't return as an error, it should be handled more gracefully in dapr
		require.False(t, testResults.MainCallSuccessful)
		require.Equal(t, 500, status)
		require.Nil(t, testResults.RawBody)
		require.Nil(t, err)
		require.NotNil(t, testResults.RawError)
		require.Contains(t, testResults.RawError, "rpc error: code = NotFound desc = Not Found")
	})

	t.Run("missing_service_http", func(t *testing.T) {
		body, err := json.Marshal(testCommandRequest{
			RemoteApp:        "missing-service-0",
			Method:           "posthandler",
			RemoteAppTracing: "true",
		})
		require.NoError(t, err)

		resp, status, err := utils.HTTPPostWithStatus(fmt.Sprintf("http://%s/badservicecalltesthttp", externalURL), body)

		var testResults negativeTestResult
		json.Unmarshal(resp, &testResults)

		// TODO: This doesn't return as an error, it should be handled more gracefully in dapr
		require.False(t, testResults.MainCallSuccessful)
		require.Equal(t, 500, status)
		require.Contains(t, string(testResults.RawBody), "failed to invoke target missing-service-0 after 3 retries")
		require.Nil(t, err)
	})

	t.Run("missing_service_grpc", func(t *testing.T) {
		body, err := json.Marshal(testCommandRequest{
			RemoteApp:        "missing-service-0",
			Method:           "posthandler",
			RemoteAppTracing: "true",
		})
		require.NoError(t, err)

		resp, status, err := utils.HTTPPostWithStatus(fmt.Sprintf("http://%s/badservicecalltestgrpc", externalURL), body)

		var testResults negativeTestResult
		json.Unmarshal(resp, &testResults)

		// TODO: This doesn't return as an error, it should be handled more gracefully in dapr
		require.False(t, testResults.MainCallSuccessful)
		require.Equal(t, 500, status)
		require.Nil(t, testResults.RawBody)
		require.Nil(t, err)
		require.NotNil(t, testResults.RawError)
		require.Contains(t, testResults.RawError, "failed to invoke target missing-service-0 after 3 retries")
	})

	t.Run("service_timeout_http", func(t *testing.T) {
		body, err := json.Marshal(testCommandRequest{
			RemoteApp:        "serviceinvocation-callee-0",
			Method:           "timeouterror",
			RemoteAppTracing: "true",
		})
		require.NoError(t, err)

		resp, status, err := utils.HTTPPostWithStatus(fmt.Sprintf("http://%s/badservicecalltesthttp", externalURL), body)

		var testResults negativeTestResult
		json.Unmarshal(resp, &testResults)

		require.False(t, testResults.MainCallSuccessful)
		require.Equal(t, 500, status)
		require.Contains(t, string(testResults.RawError), "Client.Timeout exceeded while awaiting headers")
		require.NotContains(t, string(testResults.RawError), "Client waited longer than it should have.")
	})

	t.Run("service_timeout_grpc", func(t *testing.T) {
		body, err := json.Marshal(testCommandRequest{
			RemoteApp:        "serviceinvocation-callee-0",
			Method:           "timeouterror",
			RemoteAppTracing: "true",
		})
		require.NoError(t, err)

		resp, status, err := utils.HTTPPostWithStatus(fmt.Sprintf("http://%s/badservicecalltestgrpc", externalURL), body)

		var testResults negativeTestResult
		json.Unmarshal(resp, &testResults)

		require.False(t, testResults.MainCallSuccessful)
		require.Equal(t, 500, status)
		require.Contains(t, string(testResults.RawError), "rpc error: code = DeadlineExceeded desc = context deadline exceeded")
		require.NotContains(t, string(testResults.RawError), "Client waited longer than it should have.")
	})

	t.Run("service_parse_error_http", func(t *testing.T) {
		body, err := json.Marshal(testCommandRequest{
			RemoteApp:        "serviceinvocation-callee-0",
			Method:           "parseerror",
			RemoteAppTracing: "true",
		})
		require.NoError(t, err)

		resp, status, err := utils.HTTPPostWithStatus(fmt.Sprintf("http://%s/badservicecalltesthttp", externalURL), body)

		var testResults negativeTestResult
		json.Unmarshal(resp, &testResults)

		require.False(t, testResults.MainCallSuccessful)
		require.Equal(t, 500, status)
		require.Contains(t, string(testResults.RawBody), "serialization failed with json")
		require.Nil(t, err)
	})

	t.Run("service_parse_error_grpc", func(t *testing.T) {
		body, err := json.Marshal(testCommandRequest{
			RemoteApp:        "serviceinvocation-callee-0",
			Method:           "parseerror",
			RemoteAppTracing: "true",
		})
		require.NoError(t, err)

		resp, status, err := utils.HTTPPostWithStatus(fmt.Sprintf("http://%s/badservicecalltestgrpc", externalURL), body)

		var testResults negativeTestResult
		json.Unmarshal(resp, &testResults)

		require.False(t, testResults.MainCallSuccessful)
		require.Equal(t, 500, status)
		require.Nil(t, err)
		require.Nil(t, testResults.RawBody)
		require.Contains(t, string(testResults.RawError), "rpc error: code = Unknown desc = Internal Server Error")
	})

	t.Run("service_large_data_http", func(t *testing.T) {
		body, err := json.Marshal(testCommandRequest{
			RemoteApp:        "serviceinvocation-callee-0",
			Method:           "largedatahttp",
			RemoteAppTracing: "true",
		})
		require.NoError(t, err)

		resp, err := utils.HTTPPost(fmt.Sprintf("http://%s/badservicecalltesthttp", externalURL), body)

		var testResults negativeTestResult
		json.Unmarshal(resp, &testResults)

		require.Nil(t, err)
		require.True(t, testResults.MainCallSuccessful)
		require.Len(t, testResults.Results, 4)

		for _, result := range testResults.Results {
			switch result.TestCase {
			case "1MB":
				require.True(t, result.CallSuccessful)
			case "4MB":
				require.True(t, result.CallSuccessful)
			case "4MB+":
				require.False(t, result.CallSuccessful)
			case "8MB":
				require.False(t, result.CallSuccessful)
			}
		}
	})

	t.Run("service_large_data_grpc", func(t *testing.T) {
		body, err := json.Marshal(testCommandRequest{
			RemoteApp:        "serviceinvocation-callee-0",
			Method:           "largedatagrpc",
			RemoteAppTracing: "true",
		})
		require.NoError(t, err)

		resp, err := utils.HTTPPost(fmt.Sprintf("http://%s/badservicecalltestgrpc", externalURL), body)

		var testResults negativeTestResult
		json.Unmarshal(resp, &testResults)

		require.Nil(t, err)
		require.True(t, testResults.MainCallSuccessful)
		require.Len(t, testResults.Results, 4)

		for _, result := range testResults.Results {
			switch result.TestCase {
			case "1MB":
				require.True(t, result.CallSuccessful)
			case "4MB":
				require.True(t, result.CallSuccessful)
			case "4MB+":
				require.False(t, result.CallSuccessful)
			case "8MB":
				require.False(t, result.CallSuccessful)
			}
		}
	})
}
