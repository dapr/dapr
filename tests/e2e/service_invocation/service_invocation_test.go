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

package serviceinvocation_tests

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	guuid "github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	cryptotest "github.com/dapr/kit/crypto/test"
	kitstrings "github.com/dapr/kit/strings"
	apiv1 "k8s.io/api/core/v1"
)

type testCommandRequest struct {
	RemoteApp        string  `json:"remoteApp,omitempty"`
	Method           string  `json:"method,omitempty"`
	RemoteAppTracing string  `json:"remoteAppTracing"`
	Message          *string `json:"message"`
}

type testCommandRequestExternal struct {
	testCommandRequest `json:",inline"`
	ExternalIP         string `json:"externalIP,omitempty"`
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

const (
	numHealthChecks = 60 // Number of times to call the endpoint to check for health.
)

var (
	tr                 *runner.TestRunner
	secondaryNamespace = "dapr-tests-2"
)

func TestMain(m *testing.M) {
	utils.SetupLogs("service_invocation")
	utils.InitHTTPClient(false)

	pki, err := cryptotest.GenPKIError(cryptotest.PKIOptions{
		LeafDNS: "service-invocation-external",
	})
	if err != nil {
		fmt.Println(err)
		os.Exit(-1)
	}

	secrets := []kube.SecretDescription{
		{
			Name:      "external-tls",
			Namespace: kube.DaprTestNamespace,
			Data: map[string][]byte{
				"ca.crt":  pki.RootCertPEM,
				"tls.crt": pki.LeafCertPEM,
				"tls.key": pki.LeafPKPEM,
			},
		},
		{
			Name:      "dapr-tls-client",
			Namespace: kube.DaprTestNamespace,
			Data: map[string][]byte{
				"ca.crt":  pki.RootCertPEM,
				"tls.crt": pki.ClientCertPEM,
				"tls.key": pki.ClientPKPEM,
			},
		},
	}

	// These apps will be deployed for hellodapr test before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:        "serviceinvocation-caller",
			DaprEnabled:    true,
			ImageName:      "e2e-service_invocation",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
		},
		{
			AppName:        "serviceinvocation-callee-0",
			DaprEnabled:    true,
			ImageName:      "e2e-service_invocation",
			Replicas:       1,
			MetricsEnabled: true,
		},
		{
			AppName:        "serviceinvocation-callee-1",
			DaprEnabled:    true,
			ImageName:      "e2e-service_invocation",
			Replicas:       1,
			MetricsEnabled: true,
		},
		{
			AppName:        "serviceinvocation-callee-2",
			DaprEnabled:    true,
			ImageName:      "e2e-service_invocation",
			Replicas:       2,
			MetricsEnabled: true,
			Config:         "app-channel-pipeline",
		},
		{
			AppName:        "grpcapp",
			DaprEnabled:    true,
			ImageName:      "e2e-service_invocation_grpc",
			Replicas:       1,
			MetricsEnabled: true,
			AppProtocol:    "grpc",
		},
		{
			AppName:        "secondary-ns-http",
			DaprEnabled:    true,
			ImageName:      "e2e-service_invocation",
			Replicas:       1,
			MetricsEnabled: true,
			Namespace:      &secondaryNamespace,
		},
		{
			AppName:        "secondary-ns-grpc",
			DaprEnabled:    true,
			ImageName:      "e2e-service_invocation_grpc",
			Replicas:       1,
			MetricsEnabled: true,
			Namespace:      &secondaryNamespace,
			AppProtocol:    "grpc",
		},
		{
			AppName:          "grpcproxyclient",
			DaprEnabled:      true,
			ImageName:        "e2e-service_invocation_grpc_proxy_client",
			Replicas:         1,
			IngressEnabled:   true,
			MetricsEnabled:   true,
			MaxRequestSizeMB: 6,
		},
		{
			AppName:           "grpcproxyserver",
			DaprEnabled:       true,
			ImageName:         "e2e-service_invocation_grpc_proxy_server",
			Replicas:          1,
			MetricsEnabled:    true,
			AppProtocol:       "grpc",
			AppPort:           50051,
			AppChannelAddress: "grpcproxyserver-app",
			MaxRequestSizeMB:  6,
		},
		{
			AppName:        "grpcproxyserverexternal",
			DaprEnabled:    false,
			ImageName:      "e2e-service_invocation_grpc_proxy_server",
			Replicas:       1,
			MetricsEnabled: true,
			AppProtocol:    "grpc",
			AppPort:        50051,
		},
	}

	if !kitstrings.IsTruthy(os.Getenv("SKIP_EXTERNAL_INVOCATION")) {
		testApps = append(testApps,
			kube.AppDescription{
				AppName:        "serviceinvocation-callee-external",
				DaprEnabled:    false,
				ImageName:      "e2e-service_invocation_external",
				Replicas:       1,
				IngressEnabled: true,
				MetricsEnabled: true,
				AppProtocol:    "http",
				Volumes: []apiv1.Volume{
					{
						Name: "secret-volume",
						VolumeSource: apiv1.VolumeSource{
							Secret: &apiv1.SecretVolumeSource{
								SecretName: "external-tls",
							},
						},
					},
				},
				AppVolumeMounts: []apiv1.VolumeMount{
					{
						Name:      "secret-volume",
						MountPath: "/tmp/testdata/certs",
					},
				},
			},
		)
	}

	tr = runner.NewTestRunner("hellodapr", testApps, nil, nil)
	tr.AddSecrets(secrets)
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

var serviceinvocationPathTests = []struct {
	in               string
	remoteApp        string
	appMethod        string
	expectedResponse string
}{
	{
		"Test call double encoded path",
		"serviceinvocation-callee",
		"path/value%252F123",
		"/path/value%252F123",
	},
	{
		"Test call encoded path",
		"serviceinvocation-callee",
		"path/value%2F123",
		"/path/value%2F123",
	},
	{
		"Test call normal path",
		"serviceinvocation-callee",
		"path/value/123",
		"/path/value/123",
	},
}

var serviceInvocationRedirectTests = []struct {
	in                 string
	remoteApp          string
	appMethod          string
	expectedResponse   string
	expectedStatusCode int
}{
	{
		"Test call redirect 307 Api",
		"serviceinvocation-callee",
		"opRedirect",
		"opRedirect is called",
		307,
	},
}

var moreServiceinvocationTests = []struct {
	in               string
	path             string
	remoteApp        string
	appMethod        string
	expectedResponse string
}{
	// For descriptions, see corresponding methods in dapr/tests/apps/service_invocation/app.go
	{
		"Test HTTP to HTTP",
		"httptohttptest",
		"serviceinvocation-callee-1",
		"httptohttptest",
		"success",
	},
	{
		"Test HTTP to gRPC",
		"httptogrpctest",
		"grpcapp",
		"httptogrpctest",
		"success",
	},
	{
		"Test gRPC to HTTP",
		"grpctohttptest",
		"serviceinvocation-callee-1",
		"grpctohttptest",
		"success",
	},
	{
		"Test gRPC to gRPC",
		"grpctogrpctest",
		"grpcapp",
		"grpcToGrpcTest",
		"success",
	},
}

var externalServiceInvocationTests = []struct {
	in               string
	path             string
	remoteApp        string
	appMethod        string
	expectedResponse string
	testType         string
}{
	// For descriptions, see corresponding methods in dapr/tests/apps/service_invocation/app.go
	{
		"Test HTTP to HTTP Externally using overwritten URLs",
		"/httptohttptest_external",
		"serviceinvocation-callee-external",
		"externalInvocation",
		"success",
		"URL",
	},
	{
		"Test HTTP to HTTP Externally using HTTP Endpoint CRD",
		"/httptohttptest_external",
		"external-http-endpoint",
		"externalInvocation",
		"success",
		"CRD",
	},
	{
		"Test HTTP to HTTPS Externally using HTTP Endpoint CRD",
		"/httptohttptest_external",
		"external-http-endpoint-tls",
		"externalInvocation",
		"success",
		"TLS",
	},
}

var crossNamespaceTests = []struct {
	in               string
	path             string
	remoteApp        string
	appMethod        string
	expectedResponse string
}{
	// For descriptions, see corresponding methods in dapr/tests/apps/service_invocation/app.go
	{
		"Test HTTP to HTTP",
		"httptohttptest",
		"secondary-ns-http",
		"httptohttptest",
		"success",
	},
	{
		"Test HTTP to gRPC",
		"httptogrpctest",
		"secondary-ns-grpc",
		"httptogrpctest",
		"success",
	},
	{
		"Test gRPC to HTTP",
		"grpctohttptest",
		"secondary-ns-http",
		"grpctohttptest",
		"success",
	},
	{
		"Test gRPC to gRPC",
		"grpctogrpctest",
		"secondary-ns-grpc",
		"grpcToGrpcTest",
		"success",
	},
}

var grpcProxyTests = []struct {
	in               string
	remoteApp        string
	appMethod        string
	expectedResponse string
}{
	{
		"Test grpc proxy",
		"grpcproxyclient",
		"",
		"success",
	},
	{
		"Test grpc proxy",
		"grpcproxyclient",
		"maxsize",
		"success",
	},
}

func TestServiceInvocation(t *testing.T) {
	testFn := func(targetApp string) func(t *testing.T) {
		return func(t *testing.T) {
			externalURL := tr.Platform.AcquireAppExternalURL(targetApp)
			require.NotEmpty(t, externalURL, "external URL must not be empty!")
			var err error
			// This initial probe makes the test wait a little bit longer when needed,
			// making this test less flaky due to delays in the deployment.
			_, err = utils.HTTPGetNTimes(externalURL, numHealthChecks)
			require.NoError(t, err)

			t.Logf("externalURL is '%s'", externalURL)

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
					t.Logf("unmarshalling..%s", string(resp))
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

					url := fmt.Sprintf("http://%s/%s", externalURL, tt.path)

					t.Logf("url is '%s'", url)
					resp, err := utils.HTTPPost(
						url,
						body)

					t.Log("checking err...")
					require.NoError(t, err)

					var appResp appResponse
					t.Logf("unmarshalling..%s", string(resp))
					err = json.Unmarshal(resp, &appResp)
					require.NoError(t, err)
					require.Equal(t, tt.expectedResponse, appResp.Message)
				})
			}

			// make sure dapr do not auto unescape path
			for _, tt := range serviceinvocationPathTests {
				t.Run(tt.in, func(t *testing.T) {
					body, err := json.Marshal(testCommandRequest{
						RemoteApp: tt.remoteApp,
						Method:    tt.appMethod,
					})
					require.NoError(t, err)

					url := fmt.Sprintf("http://%s/%s", externalURL, tt.appMethod)
					t.Logf("url is '%s'", url)
					resp, err := utils.HTTPPost(
						url,
						body)
					t.Log("checking err...")
					require.NoError(t, err)

					var appResp appResponse
					t.Logf("unmarshalling..%s", string(resp))
					err = json.Unmarshal(resp, &appResp)
					require.NoError(t, err)
					require.Equal(t, tt.expectedResponse, appResp.Message)
				})
			}

			// test redirect
			for _, tt := range serviceInvocationRedirectTests {
				t.Run(tt.in, func(t *testing.T) {
					body, err := json.Marshal(testCommandRequest{
						RemoteApp: tt.remoteApp,
						Method:    tt.appMethod,
					})
					require.NoError(t, err)

					url := fmt.Sprintf("http://%s/%s", externalURL, tt.appMethod)
					t.Logf("url is '%s'", url)
					resp, code, err := utils.HTTPPostWithStatus(
						url,
						body)
					t.Log("checking err...")
					require.NoError(t, err)

					var appResp appResponse
					t.Logf("unmarshalling..%s", string(resp))
					err = json.Unmarshal(resp, &appResp)
					require.NoError(t, err)
					require.Equal(t, tt.expectedResponse, appResp.Message)
					require.Equal(t, tt.expectedStatusCode, code)
				})
			}
		}
	}

	t.Run("serviceinvocation-caller", testFn("serviceinvocation-caller"))
}

func TestServiceInvocationExternally(t *testing.T) {
	if kitstrings.IsTruthy(os.Getenv("SKIP_EXTERNAL_INVOCATION")) {
		t.Skip()
	}

	testFn := func(targetApp string) func(t *testing.T) {
		return func(t *testing.T) {
			externalURL := tr.Platform.AcquireAppExternalURL(targetApp)
			require.NotEmpty(t, externalURL, "external URL must not be empty!")
			t.Logf("ExternalURL is '%s'", externalURL)

			healthCheckURLs := []string{externalURL}

			// External address is hardcoded
			invokeExternalServiceAddress := "http://service-invocation-external:80"
			if kubePlatform, ok := tr.Platform.(*runner.KubeTestPlatform); ok {
				// To perform healthchecks, we need to first get the Load Balancer address
				// Our tests will still use the hostname within the cluster for reliability reasons
				app := kubePlatform.AppResources.FindActiveResource("serviceinvocation-callee-external").(*kube.AppManager)
				svc, err := app.WaitUntilServiceState("service-invocation-external", app.IsServiceIngressReady)
				require.NoError(t, err)

				extURL := app.AcquireExternalURLFromService(svc)
				if extURL != "" {
					t.Logf("External service's load balancer is '%s'", extURL)
					healthCheckURLs = append(healthCheckURLs, extURL)
				}
			}

			// Perform healthchecks to ensure apps are ready
			err := utils.HealthCheckApps(healthCheckURLs...)
			require.NoError(t, err)

			// invoke via overwritten URL to non-Daprized service
			for _, tt := range externalServiceInvocationTests {
				testCommandReq := testCommandRequest{
					RemoteApp: tt.remoteApp,
					Method:    tt.appMethod,
				}
				switch tt.testType {
				case "URL":
					// test using overwritten URLs
					t.Run(tt.in, func(t *testing.T) {
						body, err := json.Marshal(testCommandRequestExternal{
							testCommandRequest: testCommandReq,
							ExternalIP:         invokeExternalServiceAddress,
						})
						require.NoError(t, err)
						t.Logf("invoking post to http://%s%s", externalURL, tt.path)

						resp, err := utils.HTTPPost(
							fmt.Sprintf("http://%s%s", externalURL, tt.path), body)
						t.Log("checking err...")
						require.NoError(t, err)

						var appResp appResponse
						t.Logf("unmarshalling..%s", string(resp))
						err = json.Unmarshal(resp, &appResp)
						t.Logf("appResp %s", appResp)
						require.NoError(t, err)
						require.Equal(t, tt.expectedResponse, appResp.Message)
					})
				case "CRD":
					// invoke via HTTPEndpoint CRD
					t.Run(tt.in, func(t *testing.T) {
						body, err := json.Marshal(testCommandRequestExternal{
							testCommandRequest: testCommandReq,
						})
						require.NoError(t, err)

						resp, err := utils.HTTPPost(
							fmt.Sprintf("http://%s%s", externalURL, tt.path), body)
						t.Log("checking err...")
						require.NoError(t, err)

						var appResp appResponse
						t.Logf("unmarshalling..%s", string(resp))
						err = json.Unmarshal(resp, &appResp)
						t.Logf("appResp %s", appResp)
						require.NoError(t, err)
						require.Equal(t, tt.expectedResponse, appResp.Message)
					})
				case "TLS":
					// invoke via HTTPEndpoint CRD with TLS
					t.Run(tt.in, func(t *testing.T) {
						body, err := json.Marshal(testCommandRequestExternal{
							testCommandRequest: testCommandReq,
						})
						require.NoError(t, err)

						resp, err := utils.HTTPPost(
							fmt.Sprintf("http://%s%s", externalURL, tt.path), body)
						t.Log("checking err...")
						require.NoError(t, err)

						var appResp appResponse
						t.Logf("unmarshalling..%s", string(resp))
						err = json.Unmarshal(resp, &appResp)
						t.Logf("appResp %s", appResp)
						require.NoError(t, err)
						require.Equal(t, tt.expectedResponse, appResp.Message)
					})
				}
			}
		}
	}
	t.Run("serviceinvocation-callee-external", testFn("serviceinvocation-caller"))
}

func TestGRPCProxy(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL("grpcproxyclient")
	require.NotEmpty(t, externalURL, "external URL must not be empty!")
	var err error
	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err = utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	t.Logf("externalURL is '%s'\n", externalURL)

	for _, tt := range grpcProxyTests {
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

func TestHeadersExternal(t *testing.T) {
	if kitstrings.IsTruthy(os.Getenv("SKIP_EXTERNAL_INVOCATION")) {
		t.Skip()
	}

	targetApp := "serviceinvocation-caller"
	externalURL := tr.Platform.AcquireAppExternalURL(targetApp)
	require.NotEmpty(t, externalURL, "external URL must not be empty!")

	hostname := "serviceinvocation-callee-external"
	hostnameCRD := "external-http-endpoint"

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)
	t.Logf("externalURL is '%s'\n", externalURL)

	t.Run("http-to-http-v1-using-external-invocation-overwritten-url", func(t *testing.T) {
		url := fmt.Sprintf("http://%s/tests/v1_httptohttptest_external", externalURL)
		verifyHTTPToHTTPExternal(t, externalURL, targetApp, url, hostname)
	})

	t.Run("http-to-http-v1-using-external-invocation-crd", func(t *testing.T) {
		url := fmt.Sprintf("http://%s/tests/v1_httptohttptest_external", externalURL)
		verifyHTTPToHTTPExternal(t, externalURL, targetApp, url, hostnameCRD)
	})
}

func TestHeaders(t *testing.T) {
	testFn := func(targetApp string) func(t *testing.T) {
		return func(t *testing.T) {
			externalURL := tr.Platform.AcquireAppExternalURL(targetApp)
			require.NotEmpty(t, externalURL, "external URL must not be empty!")
			hostname, hostIP, err := tr.Platform.GetAppHostDetails(targetApp)
			require.NoError(t, err, "error retrieving host details: %s", err)

			expectedForwarded := fmt.Sprintf("for=%s;by=%s;host=%s", hostIP, hostIP, hostname)

			// This initial probe makes the test wait a little bit longer when needed,
			// making this test less flaky due to delays in the deployment.
			_, err = utils.HTTPGetNTimes(externalURL, numHealthChecks)
			require.NoError(t, err)

			t.Logf("externalURL is '%s'\n", externalURL)

			t.Run("http-to-http-v1", func(t *testing.T) {
				url := fmt.Sprintf("http://%s/tests/v1_httptohttptest", externalURL)
				verifyHTTPToHTTP(t, hostIP, hostname, url, expectedForwarded, "serviceinvocation-callee-0")
			})

			t.Run("http-to-http-dapr-app-id", func(t *testing.T) {
				url := fmt.Sprintf("http://%s/tests/dapr_id_httptohttptest", externalURL)
				verifyHTTPToHTTP(t, hostIP, hostname, url, expectedForwarded, "serviceinvocation-callee-0")
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

				actualHeaders := struct {
					Request  string `json:"request"`
					Response string `json:"response"`
					Trailers string `json:"trailers"`
				}{}
				err = json.Unmarshal([]byte(appResp.Message), &actualHeaders)
				require.NoError(t, err, "failed to unmarshal response: %s", appResp.Message)
				requestHeaders := map[string][]string{}
				responseHeaders := map[string][]string{}
				trailerHeaders := map[string][]string{}
				json.Unmarshal([]byte(actualHeaders.Request), &requestHeaders)
				json.Unmarshal([]byte(actualHeaders.Response), &responseHeaders)
				json.Unmarshal([]byte(actualHeaders.Trailers), &trailerHeaders)

				require.NoError(t, err)
				_ = assert.NotEmpty(t, requestHeaders["content-type"]) &&
					assert.Equal(t, "application/grpc", requestHeaders["content-type"][0])
				_ = assert.NotEmpty(t, requestHeaders[":authority"]) &&
					assert.Equal(t, "127.0.0.1:3000", requestHeaders[":authority"][0])
				_ = assert.NotEmpty(t, requestHeaders["daprtest-request-1"]) &&
					assert.Equal(t, "DaprValue1", requestHeaders["daprtest-request-1"][0])
				_ = assert.NotEmpty(t, requestHeaders["daprtest-request-2"]) &&
					assert.Equal(t, "DaprValue2", requestHeaders["daprtest-request-2"][0])
				_ = assert.NotEmpty(t, requestHeaders["daprtest-multi"]) &&
					assert.Equal(t, []string{"M'illumino", "d'immenso"}, requestHeaders["daprtest-multi"])
				_ = assert.NotEmpty(t, requestHeaders["user-agent"]) &&
					assert.NotNil(t, requestHeaders["user-agent"][0])
				grpcTraceBinRq := requestHeaders["grpc-trace-bin"]
				if assert.NotNil(t, grpcTraceBinRq, "grpc-trace-bin is missing from the request") {
					if assert.Equal(t, 1, len(grpcTraceBinRq), "grpc-trace-bin is missing from the request") {
						assert.NotEqual(t, "", grpcTraceBinRq[0], "grpc-trace-bin is missing from the request")
					}
				}
				traceParentRq := requestHeaders["traceparent"]
				if assert.NotNil(t, traceParentRq, "traceparent is missing from the request") {
					if assert.Equal(t, 1, len(traceParentRq), "traceparent is missing from the request") {
						assert.NotEqual(t, "", traceParentRq[0], "traceparent is missing from the request")
					}
				}
				_ = assert.NotEmpty(t, requestHeaders["x-forwarded-for"]) &&
					assert.Equal(t, hostIP, requestHeaders["x-forwarded-for"][0])
				_ = assert.NotEmpty(t, requestHeaders["x-forwarded-host"]) &&
					assert.Equal(t, hostname, requestHeaders["x-forwarded-host"][0])
				_ = assert.NotEmpty(t, requestHeaders["forwarded"]) &&
					assert.Equal(t, expectedForwarded, requestHeaders["forwarded"][0])

				_ = assert.NotEmpty(t, requestHeaders[invokev1.CallerIDHeader]) &&
					assert.Equal(t, targetApp, requestHeaders[invokev1.CallerIDHeader][0])
				_ = assert.NotEmpty(t, requestHeaders[invokev1.CalleeIDHeader]) &&
					assert.Equal(t, "grpcapp", requestHeaders[invokev1.CalleeIDHeader][0])

				_ = assert.NotEmpty(t, responseHeaders["content-type"]) &&
					assert.Equal(t, "application/grpc", responseHeaders["content-type"][0])
				_ = assert.NotEmpty(t, responseHeaders["daprtest-response-1"]) &&
					assert.Equal(t, "DaprTest-Response-Value-1", responseHeaders["daprtest-response-1"][0])
				_ = assert.NotEmpty(t, responseHeaders["daprtest-response-2"]) &&
					assert.Equal(t, "DaprTest-Response-Value-2", responseHeaders["daprtest-response-2"][0])
				_ = assert.NotEmpty(t, responseHeaders["daprtest-response-multi"]) &&
					assert.Equal(t, []string{"DaprTest-Response-Multi-1", "DaprTest-Response-Multi-2"}, responseHeaders["daprtest-response-multi"])
				grpcTraceBinRs := responseHeaders["grpc-trace-bin"]
				if assert.NotNil(t, grpcTraceBinRs, "grpc-trace-bin is missing from the response") {
					if assert.Equal(t, 1, len(grpcTraceBinRs), "grpc-trace-bin is missing from the response") {
						assert.NotEqual(t, "", grpcTraceBinRs[0], "grpc-trace-bin is missing from the response")
					}
				}
				traceParentRs := responseHeaders["traceparent"]
				if assert.NotNil(t, traceParentRs, "traceparent is missing from the response") {
					if assert.Equal(t, 1, len(traceParentRs), "traceparent is missing from the response") {
						assert.NotEqual(t, "", traceParentRs[0], "traceparent is missing from the response")
					}
				}

				_ = assert.NotEmpty(t, trailerHeaders["daprtest-trailer-1"]) &&
					assert.Equal(t, "DaprTest-Trailer-Value-1", trailerHeaders["daprtest-trailer-1"][0])
				_ = assert.NotEmpty(t, trailerHeaders["daprtest-trailer-2"]) &&
					assert.Equal(t, "DaprTest-Trailer-Value-2", trailerHeaders["daprtest-trailer-2"][0])
				_ = assert.NotEmpty(t, trailerHeaders["daprtest-trailer-multi"]) &&
					assert.Equal(t, []string{"DaprTest-Trailer-Multi-1", "DaprTest-Trailer-Multi-2"}, trailerHeaders["daprtest-trailer-multi"])
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

				actualHeaders := struct {
					Request  string `json:"request"`
					Response string `json:"response"`
				}{}
				err = json.Unmarshal([]byte(appResp.Message), &actualHeaders)
				require.NoError(t, err, "failed to unmarshal response: %s", appResp.Message)
				requestHeaders := map[string][]string{}
				responseHeaders := map[string][]string{}
				json.Unmarshal([]byte(actualHeaders.Request), &requestHeaders)
				json.Unmarshal([]byte(actualHeaders.Response), &responseHeaders)

				require.NoError(t, err)
				_ = assert.NotEmpty(t, requestHeaders["Content-Type"]) &&
					assert.Equal(t, "text/plain; utf-8", requestHeaders["Content-Type"][0])
				_ = assert.NotEmpty(t, requestHeaders["Dapr-Authority"]) &&
					assert.Equal(t, "localhost:50001", requestHeaders["Dapr-Authority"][0])
				_ = assert.NotEmpty(t, requestHeaders["Daprtest-Request-1"]) &&
					assert.Equal(t, "DaprValue1", requestHeaders["Daprtest-Request-1"][0])
				_ = assert.NotEmpty(t, requestHeaders["Daprtest-Request-2"]) &&
					assert.Equal(t, "DaprValue2", requestHeaders["Daprtest-Request-2"][0])
				_ = assert.NotEmpty(t, requestHeaders["Daprtest-Multi"]) &&
					assert.Equal(t, []string{"M'illumino", "d'immenso"}, requestHeaders["Daprtest-Multi"])
				_ = assert.NotEmpty(t, requestHeaders["Traceparent"]) &&
					assert.NotNil(t, requestHeaders["Traceparent"][0])
				_ = assert.NotEmpty(t, requestHeaders["User-Agent"]) &&
					assert.NotNil(t, requestHeaders["User-Agent"][0])
				_ = assert.NotEmpty(t, requestHeaders["X-Forwarded-For"]) &&
					assert.Equal(t, hostIP, requestHeaders["X-Forwarded-For"][0])
				_ = assert.NotEmpty(t, requestHeaders["X-Forwarded-Host"]) &&
					assert.Equal(t, hostname, requestHeaders["X-Forwarded-Host"][0])
				_ = assert.NotEmpty(t, requestHeaders["Forwarded"]) &&
					assert.Equal(t, expectedForwarded, requestHeaders["Forwarded"][0])

				_ = assert.NotEmpty(t, requestHeaders["Dapr-Caller-Namespace"]) &&
					assert.Equal(t, kube.DaprTestNamespace, requestHeaders["Dapr-Caller-Namespace"][0])
				_ = assert.NotEmpty(t, requestHeaders["Dapr-Caller-App-Id"]) &&
					assert.Equal(t, targetApp, requestHeaders["Dapr-Caller-App-Id"][0])
				_ = assert.NotEmpty(t, requestHeaders["Dapr-Callee-App-Id"]) &&
					assert.Equal(t, "serviceinvocation-callee-0", requestHeaders["Dapr-Callee-App-Id"][0])

				_ = assert.NotEmpty(t, responseHeaders["content-type"]) &&
					assert.Equal(t, "application/grpc", responseHeaders["content-type"][0])
				_ = assert.NotEmpty(t, responseHeaders["dapr-content-type"]) &&
					assert.True(t, strings.HasPrefix(responseHeaders["dapr-content-type"][0], "application/json"))
				_ = assert.NotEmpty(t, responseHeaders["dapr-date"]) &&
					assert.NotNil(t, responseHeaders["dapr-date"][0])
				_ = assert.NotEmpty(t, responseHeaders["daprtest-response-1"]) &&
					assert.Equal(t, "DaprTest-Response-Value-1", responseHeaders["daprtest-response-1"][0])
				_ = assert.NotEmpty(t, responseHeaders["daprtest-response-2"]) &&
					assert.Equal(t, "DaprTest-Response-Value-2", responseHeaders["daprtest-response-2"][0])
				_ = assert.NotEmpty(t, responseHeaders["daprtest-response-multi"]) &&
					assert.Equal(t, []string{"DaprTest-Response-Multi-1", "DaprTest-Response-Multi-2"}, responseHeaders["daprtest-response-multi"])

				grpcTraceBinRs := responseHeaders["grpc-trace-bin"]
				if assert.NotNil(t, grpcTraceBinRs, "grpc-trace-bin is missing from the response") {
					if assert.Equal(t, 1, len(grpcTraceBinRs), "grpc-trace-bin is missing from the response") {
						assert.NotEqual(t, "", grpcTraceBinRs[0], "grpc-trace-bin is missing from the response")
					}
				}
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

				actualHeaders := struct {
					Request  string `json:"request"`
					Response string `json:"response"`
				}{}
				err = json.Unmarshal([]byte(appResp.Message), &actualHeaders)
				require.NoError(t, err, "failed to unmarshal response: %s", appResp.Message)
				requestHeaders := map[string][]string{}
				responseHeaders := map[string][]string{}
				json.Unmarshal([]byte(actualHeaders.Request), &requestHeaders)
				json.Unmarshal([]byte(actualHeaders.Response), &responseHeaders)

				require.NoError(t, err)

				_ = assert.NotEmpty(t, requestHeaders["content-type"]) &&
					assert.Equal(t, "application/grpc", requestHeaders["content-type"][0])
				_ = assert.NotEmpty(t, requestHeaders[":authority"]) &&
					assert.True(t, strings.HasPrefix(requestHeaders[":authority"][0], "127.0.0.1:"))
				_ = assert.NotEmpty(t, requestHeaders["daprtest-request-1"]) &&
					assert.Equal(t, "DaprValue1", requestHeaders["daprtest-request-1"][0])
				_ = assert.NotEmpty(t, requestHeaders["daprtest-request-1"]) &&
					assert.Equal(t, "DaprValue2", requestHeaders["daprtest-request-2"][0])
				_ = assert.NotEmpty(t, requestHeaders["daprtest-multi"]) &&
					assert.Equal(t, []string{"M'illumino", "d'immenso"}, requestHeaders["daprtest-multi"])
				_ = assert.NotEmpty(t, requestHeaders["user-agent"]) &&
					assert.NotNil(t, requestHeaders["user-agent"][0])
				grpcTraceBinRq := requestHeaders["grpc-trace-bin"]
				if assert.NotNil(t, grpcTraceBinRq, "grpc-trace-bin is missing from the request") {
					if assert.Equal(t, 1, len(grpcTraceBinRq), "grpc-trace-bin is missing from the request") {
						assert.NotEqual(t, "", grpcTraceBinRq[0], "grpc-trace-bin is missing from the request")
					}
				}
				traceParentRq := requestHeaders["traceparent"]
				if assert.NotNil(t, traceParentRq, "traceparent is missing from the request") {
					if assert.Equal(t, 1, len(traceParentRq), "traceparent is missing from the request") {
						assert.NotEqual(t, "", traceParentRq[0], "traceparent is missing from the request")
					}
				}
				_ = assert.NotEmpty(t, requestHeaders["x-forwarded-for"]) &&
					assert.Equal(t, hostIP, requestHeaders["x-forwarded-for"][0])
				_ = assert.NotEmpty(t, requestHeaders["x-forwarded-host"]) &&
					assert.Equal(t, hostname, requestHeaders["x-forwarded-host"][0])
				_ = assert.NotEmpty(t, requestHeaders["forwarded"]) &&
					assert.Equal(t, expectedForwarded, requestHeaders["forwarded"][0])

				assert.Equal(t, targetApp, requestHeaders[invokev1.CallerIDHeader][0])
				assert.Equal(t, "grpcapp", requestHeaders[invokev1.CalleeIDHeader][0])

				_ = assert.NotEmpty(t, responseHeaders["Content-Type"]) &&
					assert.True(t, strings.HasPrefix(responseHeaders["Content-Type"][0], "application/json"))
				_ = assert.NotEmpty(t, responseHeaders["Date"]) &&
					assert.NotNil(t, responseHeaders["Date"][0])
				_ = assert.NotEmpty(t, responseHeaders["Daprtest-Response-1"]) &&
					assert.Equal(t, "DaprTest-Response-Value-1", responseHeaders["Daprtest-Response-1"][0])
				_ = assert.NotEmpty(t, responseHeaders["Daprtest-Response-2"]) &&
					assert.Equal(t, "DaprTest-Response-Value-2", responseHeaders["Daprtest-Response-2"][0])
				_ = assert.NotEmpty(t, responseHeaders["Daprtest-Response-Multi"]) &&
					assert.Equal(t, []string{"DaprTest-Response-Multi-1", "DaprTest-Response-Multi-2"}, responseHeaders["Daprtest-Response-Multi"])
				_ = assert.NotEmpty(t, responseHeaders["Traceparent"]) &&
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

			t.Run("http-to-http-tracing-v1", func(t *testing.T) {
				url := fmt.Sprintf("http://%s/tests/v1_httptohttptest", externalURL)
				verifyHTTPToHTTPTracing(t, url, expectedTraceID, "serviceinvocation-callee-0")
			})

			t.Run("http-to-http-tracing-dapr-id", func(t *testing.T) {
				url := fmt.Sprintf("http://%s/tests/dapr_id_httptohttptest", externalURL)
				verifyHTTPToHTTPTracing(t, url, expectedTraceID, "serviceinvocation-callee-0")
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

				actualHeaders := struct {
					Request  string `json:"request"`
					Response string `json:"response"`
					Trailers string `json:"trailers"`
				}{}
				err = json.Unmarshal([]byte(appResp.Message), &actualHeaders)
				require.NoError(t, err, "failed to unmarshal response: %s", appResp.Message)
				requestHeaders := map[string][]string{}
				responseHeaders := map[string][]string{}
				trailerHeaders := map[string][]string{}
				json.Unmarshal([]byte(actualHeaders.Request), &requestHeaders)
				json.Unmarshal([]byte(actualHeaders.Response), &responseHeaders)
				json.Unmarshal([]byte(actualHeaders.Trailers), &trailerHeaders)

				require.NoError(t, err)

				grpcTraceBinRq := requestHeaders["grpc-trace-bin"]
				if assert.NotNil(t, grpcTraceBinRq, "grpc-trace-bin is missing from the request") {
					if assert.Equal(t, 1, len(grpcTraceBinRq), "grpc-trace-bin is missing from the request") {
						assert.NotEqual(t, "", grpcTraceBinRq[0], "grpc-trace-bin is missing from the request")
					}
				}
				traceParentRq := requestHeaders["traceparent"]
				if assert.NotNil(t, traceParentRq, "traceparent is missing from the request") {
					if assert.Equal(t, 1, len(traceParentRq), "traceparent is missing from the request") {
						assert.NotEqual(t, "", traceParentRq[0], "traceparent is missing from the request")
					}
				}

				grpcTraceBinRs := responseHeaders["grpc-trace-bin"]
				if assert.NotNil(t, grpcTraceBinRs) {
					if assert.Equal(t, 1, len(grpcTraceBinRs)) {
						traceContext := grpcTraceBinRs[0]
						t.Logf("received response grpc header..%s\n", traceContext)
						assert.Equal(t, expectedEncodedTraceID, traceContext)
						decoded, _ := base64.StdEncoding.DecodeString(traceContext)
						gotSc, ok := diagUtils.SpanContextFromBinary(decoded)

						assert.True(t, ok)
						assert.NotNil(t, gotSc)
						assert.Equal(t, expectedTraceID, diag.SpanContextToW3CString(gotSc))
					}
				}
				traceParentRs := responseHeaders["traceparent"]
				if assert.NotNil(t, traceParentRs, "traceparent is missing from the response") {
					if assert.Equal(t, 1, len(traceParentRs), "traceparent is missing from the response") {
						assert.Equal(t, expectedTraceID, traceParentRs[0], "traceparent value was not expected")
					}
				}
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

				actualHeaders := struct {
					Request  string `json:"request"`
					Response string `json:"response"`
				}{}
				err = json.Unmarshal([]byte(appResp.Message), &actualHeaders)
				require.NoError(t, err, "failed to unmarshal response: %s", appResp.Message)
				requestHeaders := map[string][]string{}
				responseHeaders := map[string][]string{}
				json.Unmarshal([]byte(actualHeaders.Request), &requestHeaders)
				json.Unmarshal([]byte(actualHeaders.Response), &responseHeaders)

				require.NoError(t, err)

				grpcTraceBinRq := requestHeaders["grpc-trace-bin"]
				if assert.NotNil(t, grpcTraceBinRq, "grpc-trace-bin is missing from the request") {
					if assert.Equal(t, 1, len(grpcTraceBinRq), "grpc-trace-bin is missing from the request") {
						assert.NotEqual(t, "", grpcTraceBinRq[0], "grpc-trace-bin is missing from the request")
					}
				}
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

				actualHeaders := struct {
					Request  string `json:"request"`
					Response string `json:"response"`
				}{}
				err = json.Unmarshal([]byte(appResp.Message), &actualHeaders)
				require.NoError(t, err, "failed to unmarshal response: %s", appResp.Message)
				requestHeaders := map[string][]string{}
				responseHeaders := map[string][]string{}
				json.Unmarshal([]byte(actualHeaders.Request), &requestHeaders)
				json.Unmarshal([]byte(actualHeaders.Response), &responseHeaders)

				require.NoError(t, err)

				_ = assert.NotEmpty(t, requestHeaders["Traceparent"]) &&
					assert.NotNil(t, requestHeaders["Traceparent"][0])
				_ = assert.NotEmpty(t, requestHeaders["Daprtest-Traceid"]) &&
					assert.Equal(t, expectedTraceID, requestHeaders["Daprtest-Traceid"][0])

				grpcTraceBinRs := responseHeaders["grpc-trace-bin"]
				if assert.NotNil(t, grpcTraceBinRs, "grpc-trace-bin is missing from the response") {
					if assert.Equal(t, 1, len(grpcTraceBinRs), "grpc-trace-bin is missing from the response") {
						traceContext := grpcTraceBinRs[0]
						assert.NotEqual(t, "", traceContext)

						t.Logf("received response grpc header..%s\n", traceContext)
						assert.Equal(t, expectedEncodedTraceID, traceContext)
						decoded, _ := base64.StdEncoding.DecodeString(traceContext)
						gotSc, ok := diagUtils.SpanContextFromBinary(decoded)

						assert.True(t, ok)
						assert.NotNil(t, gotSc)
						assert.Equal(t, expectedTraceID, diag.SpanContextToW3CString(gotSc))
					}
				}
			})
		}
	}

	t.Run("serviceinvocation-caller", testFn("serviceinvocation-caller"))
}

func verifyHTTPToHTTPTracing(t *testing.T, url string, expectedTraceID string, remoteApp string) {
	body, err := json.Marshal(testCommandRequest{
		RemoteApp:        remoteApp,
		Method:           "http-to-http-tracing",
		RemoteAppTracing: "true",
	})
	require.NoError(t, err)

	resp, err := utils.HTTPPost(url, body)
	t.Log("checking err...")
	require.NoError(t, err)

	var appResp appResponse
	t.Logf("unmarshalling..%s\n", string(resp))
	err = json.Unmarshal(resp, &appResp)

	actualHeaders := struct {
		Request  string `json:"request"`
		Response string `json:"response"`
	}{}
	err = json.Unmarshal([]byte(appResp.Message), &actualHeaders)
	require.NoError(t, err, "failed to unmarshal response: %s", appResp.Message)
	requestHeaders := map[string][]string{}
	responseHeaders := map[string][]string{}
	json.Unmarshal([]byte(actualHeaders.Request), &requestHeaders)
	json.Unmarshal([]byte(actualHeaders.Response), &responseHeaders)

	require.NoError(t, err)

	_ = assert.NotEmpty(t, requestHeaders["Traceparent"]) &&
		assert.NotNil(t, requestHeaders["Traceparent"][0])
	_ = assert.NotEmpty(t, requestHeaders["Daprtest-Traceid"]) &&
		assert.Equal(t, expectedTraceID, requestHeaders["Daprtest-Traceid"][0])

	traceParentRs := responseHeaders["Traceparent"]
	if assert.NotNil(t, traceParentRs, "Traceparent is missing from the response") {
		if assert.Equal(t, 1, len(traceParentRs), "Traceparent is missing from the response") {
			assert.Equal(t, expectedTraceID, traceParentRs[0], "Traceparent value was not expected")
		}
	}
}

func verifyHTTPToHTTP(t *testing.T, hostIP string, hostname string, url string, expectedForwarded string, remoteApp string) {
	body, err := json.Marshal(testCommandRequest{
		RemoteApp: remoteApp,
		Method:    "http-to-http",
	})
	require.NoError(t, err)

	resp, err := utils.HTTPPost(url, body)
	t.Log("checking err...")
	require.NoError(t, err)

	var appResp appResponse
	t.Logf("unmarshalling..%s\n", string(resp))
	err = json.Unmarshal(resp, &appResp)

	actualHeaders := struct {
		Request  string `json:"request"`
		Response string `json:"response"`
	}{}
	err = json.Unmarshal([]byte(appResp.Message), &actualHeaders)
	require.NoError(t, err, "failed to unmarshal response: %s", appResp.Message)
	requestHeaders := map[string][]string{}
	responseHeaders := map[string][]string{}
	json.Unmarshal([]byte(actualHeaders.Request), &requestHeaders)
	json.Unmarshal([]byte(actualHeaders.Response), &responseHeaders)

	t.Logf("requestHeaders: [%#v] - responseHeaders: [%#v]", requestHeaders, responseHeaders)

	require.NoError(t, err)
	_ = assert.NotEmpty(t, requestHeaders["Content-Type"]) &&
		assert.True(t, strings.HasPrefix(requestHeaders["Content-Type"][0], "application/json"))
	_ = assert.NotEmpty(t, requestHeaders["Daprtest-Request-1"]) &&
		assert.Equal(t, "DaprValue1", requestHeaders["Daprtest-Request-1"][0])
	_ = assert.NotEmpty(t, requestHeaders["Daprtest-Request-2"]) &&
		assert.Equal(t, "DaprValue2", requestHeaders["Daprtest-Request-2"][0])
	_ = assert.NotEmpty(t, requestHeaders["Daprtest-Multi"]) &&
		assert.Equal(t, []string{"M'illumino", "d'immenso"}, requestHeaders["Daprtest-Multi"])
	_ = assert.NotEmpty(t, requestHeaders["Traceparent"]) &&
		assert.NotNil(t, requestHeaders["Traceparent"][0])
	_ = assert.NotEmpty(t, requestHeaders["User-Agent"]) &&
		assert.NotNil(t, requestHeaders["User-Agent"][0])
	_ = assert.NotEmpty(t, requestHeaders["X-Forwarded-For"]) &&
		assert.Equal(t, hostIP, requestHeaders["X-Forwarded-For"][0])
	_ = assert.NotEmpty(t, requestHeaders["X-Forwarded-Host"]) &&
		assert.Equal(t, hostname, requestHeaders["X-Forwarded-Host"][0])
	_ = assert.NotEmpty(t, requestHeaders["Forwarded"]) &&
		assert.Equal(t, expectedForwarded, requestHeaders["Forwarded"][0])

	_ = assert.NotEmpty(t, responseHeaders["Content-Type"]) &&
		assert.True(t, strings.HasPrefix(responseHeaders["Content-Type"][0], "application/json"))
	_ = assert.NotEmpty(t, responseHeaders["Daprtest-Response-1"]) &&
		assert.Equal(t, "DaprTest-Response-Value-1", responseHeaders["Daprtest-Response-1"][0])
	_ = assert.NotEmpty(t, responseHeaders["Daprtest-Response-2"]) &&
		assert.Equal(t, "DaprTest-Response-Value-2", responseHeaders["Daprtest-Response-2"][0])
	_ = assert.NotEmpty(t, responseHeaders["Daprtest-Response-Multi"]) &&
		assert.Equal(t, []string{"DaprTest-Response-Multi-1", "DaprTest-Response-Multi-2"}, responseHeaders["Daprtest-Response-Multi"])
	_ = assert.NotEmpty(t, responseHeaders["Traceparent"]) &&
		assert.NotNil(t, responseHeaders["Traceparent"][0])
}

func verifyHTTPToHTTPExternal(t *testing.T, hostIP string, hostname string, url string, remoteApp string) {
	invokeExternalServiceIP := "http://service-invocation-external"

	body, err := json.Marshal(testCommandRequestExternal{
		testCommandRequest: testCommandRequest{
			RemoteApp:        remoteApp,
			Method:           "/retrieve_request_object",
			RemoteAppTracing: "true",
		},
		ExternalIP: invokeExternalServiceIP,
	})
	require.NoError(t, err)

	resp, err := utils.HTTPPost(url, body)
	t.Log("checking err...")
	require.NoError(t, err)

	var appResp appResponse
	t.Logf("unmarshalling..%s\n", string(resp))
	err = json.Unmarshal(resp, &appResp)

	actualHeaders := struct {
		Request  string `json:"request"`
		Response string `json:"response"`
	}{}
	err = json.Unmarshal([]byte(appResp.Message), &actualHeaders)
	require.NoError(t, err, "failed to unmarshal response: %s", appResp.Message)
	requestHeaders := map[string][]string{}
	responseHeaders := map[string][]string{}
	json.Unmarshal([]byte(actualHeaders.Request), &requestHeaders)
	json.Unmarshal([]byte(actualHeaders.Response), &responseHeaders)

	t.Logf("requestHeaders: [%#v] - responseHeaders: [%#v]", requestHeaders, responseHeaders)

	require.NoError(t, err)
	_ = assert.NotEmpty(t, requestHeaders["Content-Type"]) &&
		assert.True(t, strings.HasPrefix(requestHeaders["Content-Type"][0], "application/json"))
	_ = assert.NotEmpty(t, requestHeaders["Daprtest-Request-1"]) &&
		assert.Equal(t, "DaprValue1", requestHeaders["Daprtest-Request-1"][0])
	_ = assert.NotEmpty(t, requestHeaders["Daprtest-Request-2"]) &&
		assert.Equal(t, "DaprValue2", requestHeaders["Daprtest-Request-2"][0])
	_ = assert.NotEmpty(t, requestHeaders["Daprtest-Multi"]) &&
		assert.Equal(t, []string{"M'illumino", "d'immenso"}, requestHeaders["Daprtest-Multi"])
	_ = assert.NotEmpty(t, requestHeaders["Traceparent"]) &&
		assert.NotNil(t, requestHeaders["Traceparent"][0])
	_ = assert.NotEmpty(t, requestHeaders["User-Agent"]) &&
		assert.NotNil(t, requestHeaders["User-Agent"][0])
	_ = assert.NotEmpty(t, responseHeaders["Content-Type"]) &&
		assert.True(t, strings.HasPrefix(responseHeaders["Content-Type"][0], "application/json"))
	_ = assert.NotEmpty(t, responseHeaders["Daprtest-Response-1"]) &&
		assert.Equal(t, "DaprTest-Response-Value-1", responseHeaders["Daprtest-Response-1"][0])
	_ = assert.NotEmpty(t, responseHeaders["Daprtest-Response-2"]) &&
		assert.Equal(t, "DaprTest-Response-Value-2", responseHeaders["Daprtest-Response-2"][0])
	_ = assert.NotEmpty(t, responseHeaders["Daprtest-Response-Multi"]) &&
		assert.Equal(t, []string{"DaprTest-Response-Multi-1", "DaprTest-Response-Multi-2"}, responseHeaders["Daprtest-Response-Multi"])
	_ = assert.NotEmpty(t, responseHeaders["Traceparent"]) &&
		assert.NotNil(t, responseHeaders["Traceparent"][0])
}

func TestUppercaseMiddlewareServiceInvocation(t *testing.T) {
	testFn := func(targetApp string) func(t *testing.T) {
		return func(t *testing.T) {
			externalURL := tr.Platform.AcquireAppExternalURL(targetApp)
			require.NotEmpty(t, externalURL, "external URL must not be empty!")

			// This initial probe makes the test wait a little bit longer when needed,
			// making this test less flaky due to delays in the deployment.
			_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
			require.NoError(t, err)

			t.Run("uppercase middleware should be applied", func(t *testing.T) {
				testMessage := guuid.New().String()
				body, err := json.Marshal(testCommandRequest{
					RemoteApp: "serviceinvocation-callee-2",
					Method:    "httptohttptest",
					Message:   &testMessage,
				})
				require.NoError(t, err)

				resp, err := utils.HTTPPost(
					fmt.Sprintf("%s/httptohttptest", externalURL), body)
				t.Log("checking err...")
				require.NoError(t, err)

				var appResp appResponse
				t.Logf("unmarshalling..%s\n", string(resp))
				err = json.Unmarshal(resp, &appResp)
				require.NoError(t, err)

				uppercaseMsg := strings.ToUpper(testMessage)
				require.Contains(t, appResp.Message, uppercaseMsg)
			})
		}
	}

	t.Run("serviceinvocation-caller", testFn("serviceinvocation-caller"))
}

func TestLoadBalancing(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL("serviceinvocation-caller")
	require.NotEmpty(t, externalURL, "external URL must not be empty!")

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	// Make 50 invocations and make sure that we get responses from both apps
	foundPIDs := map[string]int{}
	var total int
	for i := 0; i < 50; i++ {
		resp, err := utils.HTTPPost(fmt.Sprintf("%s/tests/loadbalancing", externalURL), nil)
		require.NoError(t, err)

		var appResp appResponse
		err = json.Unmarshal(resp, &appResp)
		require.NoErrorf(t, err, "Failed to unmarshal response: %s", string(resp))
		require.NotEmpty(t, appResp.Message)

		foundPIDs[appResp.Message]++
		total++
	}

	t.Logf("Found PIDs: %v", foundPIDs)
	assert.Equal(t, 50, total)
	assert.Len(t, foundPIDs, 2)
	for pid, count := range foundPIDs {
		// We won't have a perfect 50/50 distribution, so ensure that at least 5 requests (10%) hit each one
		assert.GreaterOrEqualf(t, count, 5, "Instance with PID %s did not get at least 5 requests", pid)
	}
}

func TestNegativeCases(t *testing.T) {
	testFn := func(targetApp string) func(t *testing.T) {
		return func(t *testing.T) {
			externalURL := tr.Platform.AcquireAppExternalURL(targetApp)
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
				require.Contains(t, string(testResults.RawBody), "failed to resolve address for 'missing-service-0-dapr.dapr-tests.svc.cluster.local'")
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
				require.Contains(t, testResults.RawError, "failed to resolve address for 'missing-service-0-dapr.dapr-tests.svc.cluster.local'")
			})

			t.Run("service_timeout_http", func(t *testing.T) {
				body, err := json.Marshal(testCommandRequest{
					RemoteApp:        "serviceinvocation-callee-0",
					Method:           "timeouterror",
					RemoteAppTracing: "true",
				})
				require.NoError(t, err)

				resp, status, _ := utils.HTTPPostWithStatus(fmt.Sprintf("http://%s/badservicecalltesthttp", externalURL), body)

				var testResults negativeTestResult
				json.Unmarshal(resp, &testResults)

				require.False(t, testResults.MainCallSuccessful)
				require.Equal(t, 500, status)
				require.Contains(t, testResults.RawError, "Client.Timeout exceeded while awaiting headers")
				require.NotContains(t, testResults.RawError, "Client waited longer than it should have.")
			})

			t.Run("service_timeout_grpc", func(t *testing.T) {
				body, err := json.Marshal(testCommandRequest{
					RemoteApp:        "serviceinvocation-callee-0",
					Method:           "timeouterror",
					RemoteAppTracing: "true",
				})
				require.NoError(t, err)

				resp, status, _ := utils.HTTPPostWithStatus(fmt.Sprintf("http://%s/badservicecalltestgrpc", externalURL), body)

				var testResults negativeTestResult
				json.Unmarshal(resp, &testResults)

				require.False(t, testResults.MainCallSuccessful)
				require.Equal(t, 500, status)
				// This error could have code either DeadlineExceeded or Internal, depending on where the context timeout was caught
				// Valid errors are:
				// - `rpc error: code = Internal desc = failed to invoke, id: serviceinvocation-callee-0, err: rpc error: code = Internal desc = error invoking app channel: Post \"http://127.0.0.1:3000/timeouterror\": context deadline exceeded``
				// - `rpc error: code = DeadlineExceeded desc = context deadline exceeded`
				assert.Contains(t, testResults.RawError, "rpc error: code = DeadlineExceeded")
				assert.NotContains(t, testResults.RawError, "Client waited longer than it should have.")
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
				require.Contains(t, testResults.RawError, "rpc error: code = Unknown desc = Internal Server Error")
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

				require.NoError(t, err)
				require.True(t, testResults.MainCallSuccessful)
				require.Len(t, testResults.Results, 4)

				for _, result := range testResults.Results {
					switch result.TestCase {
					case "1MB":
						assert.True(t, result.CallSuccessful)
					case "4MB":
						assert.True(t, result.CallSuccessful)
					case "4MB+":
						assert.False(t, result.CallSuccessful)
					case "8MB":
						assert.False(t, result.CallSuccessful)
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

				require.NoError(t, err)
				require.True(t, testResults.MainCallSuccessful)
				require.Len(t, testResults.Results, 4)

				for _, result := range testResults.Results {
					switch result.TestCase {
					case "1MB":
						assert.True(t, result.CallSuccessful)
					case "4MB":
						assert.True(t, result.CallSuccessful)
					case "4MB+":
						assert.False(t, result.CallSuccessful)
					case "8MB":
						assert.False(t, result.CallSuccessful)
					}
				}
			})
		}
	}

	t.Run("serviceinvocation-caller", testFn("serviceinvocation-caller"))
}

func TestNegativeCasesExternal(t *testing.T) {
	if kitstrings.IsTruthy(os.Getenv("SKIP_EXTERNAL_INVOCATION")) {
		t.Skip()
	}

	testFn := func(targetApp string) func(t *testing.T) {
		return func(t *testing.T) {
			externalURL := tr.Platform.AcquireAppExternalURL(targetApp)
			require.NotEmpty(t, externalURL, "external URL must not be empty!")
			externalServiceName := "serviceinvocation-callee-external"
			invokeExternalServiceIP := tr.Platform.AcquireAppExternalURL(externalServiceName)
			// hostNameCRD := "service-invocation-external-via-crd"
			// // This initial probe makes the test wait a little bit longer when needed,
			// // making this test less flaky due to delays in the deployment.
			// _, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
			// require.NoError(t, err)

			// t.Logf("externalURL is '%s'\n", externalURL)

			t.Run("missing_method_http", func(t *testing.T) {
				body, err := json.Marshal(testCommandRequest{
					RemoteApp:        utils.SanitizeHTTPURL(invokeExternalServiceIP),
					Method:           "missing",
					RemoteAppTracing: "true",
				})
				require.NoError(t, err)

				resp, status, err := utils.HTTPPostWithStatus(fmt.Sprintf("http://%s/badservicecalltesthttp", externalURL), body)

				var testResults negativeTestResult
				require.NoError(t, json.Unmarshal(resp, &testResults))

				// TODO: This doesn't return as an error, it should be handled more gracefully in dapr
				require.False(t, testResults.MainCallSuccessful)
				require.Equal(t, http.StatusNotFound, status)
				require.Contains(t, string(testResults.RawBody), "404 page not found")
				require.Nil(t, err)
			})

			/*TODO(@Sam): this is giving a 500 not 404
			  t.Run("missing_method_http - http endpoint CRD name", func(t *testing.T) {
			  	body, err := json.Marshal(testCommandRequest{
			  		RemoteApp:        hostNameCRD,
			  		Method:           "missing",
			  		RemoteAppTracing: "true",
			  	})
			  	require.NoError(t, err)

			  	resp, status, err := utils.HTTPPostWithStatus(fmt.Sprintf("http://%s/badservicecalltesthttp", externalURL), body)

			  	var testResults negativeTestResult
			  	require.NoError(t, json.Unmarshal(resp, &testResults), err)

			  	// TODO: This doesn't return as an error, it should be handled more gracefully in dapr
			  	require.False(t, testResults.MainCallSuccessful)
			  	require.Equal(t, http.StatusNotFound, status)
			  	require.Contains(t, string(testResults.RawBody), "404 page not found")
			  	require.Nil(t, err)
			  })*/

			// TODO(@Sam): test service timeout, parse error from service, and large data
		}
	}

	t.Run("serviceinvocation-caller", testFn("serviceinvocation-caller"))
}

func TestCrossNamespaceCases(t *testing.T) {
	testFn := func(targetApp string) func(t *testing.T) {
		return func(t *testing.T) {
			externalURL := tr.Platform.AcquireAppExternalURL(targetApp)
			require.NotEmpty(t, externalURL, "external URL must not be empty!")

			// This initial probe makes the test wait a little bit longer when needed,
			// making this test less flaky due to delays in the deployment.
			_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
			require.NoError(t, err)

			t.Logf("externalURL is '%s'\n", externalURL)

			for _, tt := range crossNamespaceTests {
				remoteAppFQ := fmt.Sprintf("%s.%s", tt.remoteApp, secondaryNamespace)
				t.Run(tt.in, func(t *testing.T) {
					body, err := json.Marshal(testCommandRequest{
						RemoteApp: remoteAppFQ,
						Method:    tt.appMethod,
					})
					require.NoError(t, err)

					url := fmt.Sprintf("http://%s/%s", externalURL, tt.path)

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
	}

	t.Run("serviceinvocation-caller", testFn("serviceinvocation-caller"))
}

func TestPathURLNormalization(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL("serviceinvocation-caller")
	require.NotEmpty(t, externalURL, "external URL must not be empty!")

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	t.Logf("externalURL is '%s'\n", externalURL)

	for path, exp := range map[string]string{
		`/foo/%2Fbbb%2F%2E`:     `/foo/%2Fbbb%2F%2E`,
		`//foo/%2Fb/bb%2F%2E`:   `/foo/%2Fb/bb%2F%2E`,
		`//foo/%2Fb///bb%2F%2E`: `/foo/%2Fb/bb%2F%2E`,
		`/foo/%2E`:              `/foo/%2E`,
		`///foo///%2E`:          `/foo/%2E`,
	} {
		t.Run(path, func(t *testing.T) {
			body, err := json.Marshal(testCommandRequest{
				RemoteApp: "serviceinvocation-callee-0",
				Method:    "normalization",
			})
			require.NoError(t, err)

			url := fmt.Sprintf("http://%s/%s", externalURL, path)
			resp, err := utils.HTTPPost(url, body)
			require.NoError(t, err)

			t.Logf("checking piped path..%s\n", string(resp))
			assert.Contains(t, exp, string(resp))
		})
	}
}
