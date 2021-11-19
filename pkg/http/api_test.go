// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

//nolint:goconst
package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	gohttp "net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/agrea/ptr"
	routing "github.com/fasthttp/router"
	jsoniter "github.com/json-iterator/go"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
	epb "google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/middleware"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/kit/logger"

	"github.com/dapr/dapr/pkg/actors"
	components_v1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/channel/http"
	http_middleware_loader "github.com/dapr/dapr/pkg/components/middleware/http"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	http_middleware "github.com/dapr/dapr/pkg/middleware/http"
	runtime_pubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	daprt "github.com/dapr/dapr/pkg/testing"
	testtrace "github.com/dapr/dapr/pkg/testing/trace"
)

var invalidJSON = []byte{0x7b, 0x7b}

func TestPubSubEndpoints(t *testing.T) {
	fakeServer := newFakeHTTPServer()
	testAPI := &api{
		pubsubAdapter: &daprt.MockPubSubAdapter{
			PublishFn: func(req *pubsub.PublishRequest) error {
				if req.PubsubName == "errorpubsub" {
					return fmt.Errorf("Error from pubsub %s", req.PubsubName)
				}

				if req.PubsubName == "errnotfound" {
					return runtime_pubsub.NotFoundError{PubsubName: "errnotfound"}
				}

				if req.PubsubName == "errnotallowed" {
					return runtime_pubsub.NotAllowedError{Topic: req.Topic, ID: "test"}
				}

				return nil
			},
			GetPubSubFn: func(pubsubName string) pubsub.PubSub {
				return &daprt.MockPubSub{}
			},
		},
		json: jsoniter.ConfigFastest,
	}
	fakeServer.StartServer(testAPI.constructPubSubEndpoints())

	t.Run("Publish successfully - 204 No Content", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/pubsubname/topic", apiVersionV1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, []byte("{\"key\": \"value\"}"), nil)
			// assert
			assert.Equal(t, 204, resp.StatusCode, "failed to publish with %s", method)
			assert.Equal(t, []byte{}, resp.RawBody, "Always give empty body with 204")
		}
	})

	t.Run("Publish multi path successfully - 204 No Content", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/pubsubname/A/B/C", apiVersionV1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, []byte("{\"key\": \"value\"}"), nil)
			// assert
			assert.Equal(t, 204, resp.StatusCode, "failed to publish with %s", method)
			assert.Equal(t, []byte{}, resp.RawBody, "Always give empty body with 204")
		}
	})

	t.Run("Publish unsuccessfully - 500 InternalError", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/errorpubsub/topic", apiVersionV1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, []byte("{\"key\": \"value\"}"), nil)
			// assert
			assert.Equal(t, 500, resp.StatusCode, "expected internal server error as response")
			assert.Equal(t, "ERR_PUBSUB_PUBLISH_MESSAGE", resp.ErrorBody["errorCode"])
		}
	})

	t.Run("Publish without topic name - 404", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/pubsubname", apiVersionV1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, []byte("{\"key\": \"value\"}"), nil)
			// assert
			assert.Equal(t, 404, resp.StatusCode, "unexpected success publishing with %s", method)
		}
	})

	t.Run("Publish without topic name ending in / - 404", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/pubsubname/", apiVersionV1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, []byte("{\"key\": \"value\"}"), nil)
			// assert
			assert.Equal(t, 404, resp.StatusCode, "unexpected success publishing with %s", method)
		}
	})

	t.Run("Publish with topic name '/' - 204", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/pubsubname//", apiVersionV1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, []byte("{\"key\": \"value\"}"), nil)
			// assert
			assert.Equal(t, 204, resp.StatusCode, "success publishing with %s", method)
		}
	})

	t.Run("Publish without topic or pubsub name - 404", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish", apiVersionV1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, []byte("{\"key\": \"value\"}"), nil)
			// assert
			assert.Equal(t, 404, resp.StatusCode, "unexpected success publishing with %s", method)
		}
	})

	t.Run("Publish without topic or pubsub name ending in / - 404", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/", apiVersionV1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, []byte("{\"key\": \"value\"}"), nil)
			// assert
			assert.Equal(t, 404, resp.StatusCode, "unexpected success publishing with %s", method)
		}
	})

	t.Run("Pubsub not configured - 400", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/pubsubname/topic", apiVersionV1)
		testMethods := []string{"POST", "PUT"}
		savePubSubAdapter := testAPI.pubsubAdapter
		testAPI.pubsubAdapter = nil
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, []byte("{\"key\": \"value\"}"), nil)
			// assert
			assert.Equal(t, 400, resp.StatusCode, "unexpected success publishing with %s", method)
			assert.Equal(t, "ERR_PUBSUB_NOT_CONFIGURED", resp.ErrorBody["errorCode"])
		}
		testAPI.pubsubAdapter = savePubSubAdapter
	})

	t.Run("Pubsub not configured - 400", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/errnotfound/topic", apiVersionV1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, []byte("{\"key\": \"value\"}"), nil)
			// assert
			assert.Equal(t, 400, resp.StatusCode, "unexpected success publishing with %s", method)
			assert.Equal(t, "ERR_PUBSUB_NOT_FOUND", resp.ErrorBody["errorCode"])
			assert.Equal(t, "pubsub 'errnotfound' not found", resp.ErrorBody["message"])
		}
	})

	t.Run("Pubsub not configured - 403", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/errnotallowed/topic", apiVersionV1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, []byte("{\"key\": \"value\"}"), nil)
			// assert
			assert.Equal(t, 403, resp.StatusCode, "unexpected success publishing with %s", method)
			assert.Equal(t, "ERR_PUBSUB_FORBIDDEN", resp.ErrorBody["errorCode"])
			assert.Equal(t, "topic topic is not allowed for app id test", resp.ErrorBody["message"])
		}
	})

	fakeServer.Shutdown()
}

func TestShutdownEndpoints(t *testing.T) {
	fakeServer := newFakeHTTPServer()

	m := mock.Mock{}
	m.On("shutdown", mock.Anything).Return()
	testAPI := &api{
		json: jsoniter.ConfigFastest,
		shutdown: func() {
			m.MethodCalled("shutdown")
		},
	}

	fakeServer.StartServer(testAPI.constructShutdownEndpoints())

	t.Run("Shutdown successfully - 204", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/shutdown", apiVersionV1)
		resp := fakeServer.DoRequest("POST", apiPath, nil, nil)
		assert.Equal(t, 204, resp.StatusCode, "success shutdown")
		for i := 0; i < 5 && len(m.Calls) == 0; i++ {
			<-time.After(200 * time.Millisecond)
		}
		m.AssertCalled(t, "shutdown")
	})

	fakeServer.Shutdown()
}

func TestGetStatusCodeFromMetadata(t *testing.T) {
	t.Run("status code present", func(t *testing.T) {
		res := GetStatusCodeFromMetadata(map[string]string{
			http.HTTPStatusCode: "404",
		})
		assert.Equal(t, 404, res, "expected status code to match")
	})
	t.Run("status code not present", func(t *testing.T) {
		res := GetStatusCodeFromMetadata(map[string]string{})
		assert.Equal(t, 200, res, "expected status code to match")
	})
	t.Run("status code present but invalid", func(t *testing.T) {
		res := GetStatusCodeFromMetadata(map[string]string{
			http.HTTPStatusCode: "a12a",
		})
		assert.Equal(t, 200, res, "expected status code to match")
	})
}

func TestGetMetadataFromRequest(t *testing.T) {
	t.Run("request with query args", func(t *testing.T) {
		// set
		ctx := &fasthttp.RequestCtx{}
		ctx.Request.SetRequestURI("http://test.example.com/resource?metadata.test=test&&other=other")
		// act
		m := getMetadataFromRequest(ctx)
		// assert
		assert.NotEmpty(t, m, "expected map to be populated")
		assert.Equal(t, 1, len(m), "expected length to match")
		assert.Equal(t, "test", m["test"], "test", "expected value to be equal")
	})
}

func TestV1OutputBindingsEndpoints(t *testing.T) {
	fakeServer := newFakeHTTPServer()
	testAPI := &api{
		sendToOutputBindingFn: func(name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error) {
			if name == "testbinding" {
				return nil, nil
			}
			return &bindings.InvokeResponse{Data: []byte("testresponse")}, nil
		},
		json: jsoniter.ConfigFastest,
	}
	fakeServer.StartServer(testAPI.constructBindingsEndpoints())

	t.Run("Invoke output bindings - 204 No Content empty response", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/bindings/testbinding", apiVersionV1)
		req := OutputBindingRequest{
			Data: "fake output",
		}
		b, _ := json.Marshal(&req)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, b, nil)
			// assert
			assert.Equal(t, 204, resp.StatusCode, "failed to invoke output binding with %s", method)
			assert.Equal(t, []byte{}, resp.RawBody, "expected response to match")
		}
	})

	t.Run("Invoke output bindings - 200 OK", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/bindings/testresponse", apiVersionV1)
		req := OutputBindingRequest{
			Data: "fake output",
		}
		b, _ := json.Marshal(&req)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, b, nil)
			// assert
			assert.Equal(t, 200, resp.StatusCode, "failed to invoke output binding with %s", method)
			assert.Equal(t, []byte("testresponse"), resp.RawBody, "expected response to match")
		}
	})

	t.Run("Invoke output bindings - 400 InternalError invalid req", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/bindings/testresponse", apiVersionV1)
		req := `{"dat" : "invalid request"}`
		b, _ := json.Marshal(&req)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, b, nil)
			// assert
			assert.Equal(t, 400, resp.StatusCode)
			assert.Equal(t, "ERR_MALFORMED_REQUEST", resp.ErrorBody["errorCode"])
		}
	})

	t.Run("Invoke output bindings - 500 InternalError", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/bindings/notfound", apiVersionV1)
		req := OutputBindingRequest{
			Data: "fake output",
		}
		b, _ := json.Marshal(&req)

		testAPI.sendToOutputBindingFn = func(name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error) {
			return nil, errors.New("missing binding name")
		}

		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, b, nil)

			// assert
			assert.Equal(t, 500, resp.StatusCode)
			assert.Equal(t, "ERR_INVOKE_OUTPUT_BINDING", resp.ErrorBody["errorCode"])
		}
	})

	fakeServer.Shutdown()
}

func TestV1OutputBindingsEndpointsWithTracer(t *testing.T) {
	fakeServer := newFakeHTTPServer()
	buffer := ""
	spec := config.TracingSpec{SamplingRate: "1"}

	createExporters(&buffer)

	testAPI := &api{
		sendToOutputBindingFn: func(name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error) { return nil, nil },
		json:                  jsoniter.ConfigFastest,
		tracingSpec:           spec,
	}
	fakeServer.StartServerWithTracing(spec, testAPI.constructBindingsEndpoints())

	t.Run("Invoke output bindings - 204 OK", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/bindings/testbinding", apiVersionV1)
		req := OutputBindingRequest{
			Data: "fake output",
		}
		b, _ := json.Marshal(&req)

		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			buffer = ""
			// act
			resp := fakeServer.DoRequest(method, apiPath, b, nil)

			// assert
			assert.Equal(t, 204, resp.StatusCode, "failed to invoke output binding with %s", method)
		}
	})

	t.Run("Invoke output bindings - 500 InternalError", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/bindings/notfound", apiVersionV1)
		req := OutputBindingRequest{
			Data: "fake output",
		}
		b, _ := json.Marshal(&req)

		testAPI.sendToOutputBindingFn = func(name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error) {
			return nil, errors.New("missing binding name")
		}

		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			buffer = ""
			// act
			resp := fakeServer.DoRequest(method, apiPath, b, nil)

			// assert
			assert.Equal(t, 500, resp.StatusCode)
			assert.Equal(t, "ERR_INVOKE_OUTPUT_BINDING", resp.ErrorBody["errorCode"])
		}
	})

	fakeServer.Shutdown()
}

func TestV1DirectMessagingEndpoints(t *testing.T) {
	headerMetadata := map[string][]string{
		"Accept-Encoding": {"gzip"},
		"Content-Length":  {"8"},
		"Content-Type":    {"application/json"},
		"Host":            {"localhost"},
		"User-Agent":      {"Go-http-client/1.1"},
	}
	fakeDirectMessageResponse := invokev1.NewInvokeMethodResponse(200, "OK", nil)
	fakeDirectMessageResponse.WithRawData([]byte("fakeDirectMessageResponse"), "application/json")

	mockDirectMessaging := new(daprt.MockDirectMessaging)

	fakeServer := newFakeHTTPServer()
	testAPI := &api{
		directMessaging: mockDirectMessaging,
		json:            jsoniter.ConfigFastest,
	}
	fakeServer.StartServer(testAPI.constructDirectMessagingEndpoints())

	t.Run("Invoke direct messaging without querystring - 200 OK", func(t *testing.T) {
		apiPath := "v1.0/invoke/fakeAppID/method/fakeMethod"
		fakeData := []byte("fakeData")

		fakeReq := invokev1.NewInvokeMethodRequest("fakeMethod")
		fakeReq.WithHTTPExtension(gohttp.MethodPost, "")
		fakeReq.WithRawData(fakeData, "application/json")
		fakeReq.WithMetadata(headerMetadata)

		mockDirectMessaging.Calls = nil // reset call count

		mockDirectMessaging.On("Invoke",
			mock.MatchedBy(func(a context.Context) bool {
				return true
			}), mock.MatchedBy(func(b string) bool {
				return b == "fakeAppID"
			}), mock.MatchedBy(func(c *invokev1.InvokeMethodRequest) bool {
				return true
			})).Return(fakeDirectMessageResponse, nil).Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, []byte("fakeDirectMessageResponse"), resp.RawBody)
	})

	t.Run("Invoke direct messaging with dapr-app-id in header - 200 OK", func(t *testing.T) {
		apiPath := "fakeMethod"
		fakeData := []byte("fakeData")

		mockDirectMessaging.Calls = nil // reset call count

		mockDirectMessaging.On("Invoke",
			mock.MatchedBy(func(a context.Context) bool {
				return true
			}), mock.MatchedBy(func(b string) bool {
				return b == "fakeAppID"
			}), mock.MatchedBy(func(c *invokev1.InvokeMethodRequest) bool {
				return true
			})).Return(fakeDirectMessageResponse, nil).Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil, "dapr-app-id", "fakeAppID")

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, []byte("fakeDirectMessageResponse"), resp.RawBody)
	})

	t.Run("Invoke direct messaging with dapr-app-id in basic auth - 200 OK", func(t *testing.T) {
		apiPath := "fakeMethod"
		fakeData := []byte("fakeData")

		mockDirectMessaging.Calls = nil // reset call count

		mockDirectMessaging.On("Invoke",
			mock.MatchedBy(func(a context.Context) bool {
				return true
			}), mock.MatchedBy(func(b string) bool {
				return b == "fakeAppID"
			}), mock.MatchedBy(func(c *invokev1.InvokeMethodRequest) bool {
				return true
			})).Return(fakeDirectMessageResponse, nil).Once()

		// act
		resp := fakeServer.doRequest("dapr-app-id:fakeAppID", "POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, []byte("fakeDirectMessageResponse"), resp.RawBody)
	})

	t.Run("Invoke direct messaging with InvalidArgument Response - 400 Bad request", func(t *testing.T) {
		d := &epb.ErrorInfo{
			Reason: "fakeReason",
		}
		details, _ := anypb.New(d)

		fakeInternalErrorResponse := invokev1.NewInvokeMethodResponse(
			int32(codes.InvalidArgument),
			"InvalidArgument",
			[]*anypb.Any{details})
		apiPath := "v1.0/invoke/fakeAppID/method/fakeMethod"
		fakeData := []byte("fakeData")

		fakeReq := invokev1.NewInvokeMethodRequest("fakeMethod")
		fakeReq.WithHTTPExtension(gohttp.MethodPost, "")
		fakeReq.WithRawData(fakeData, "application/json")
		fakeReq.WithMetadata(headerMetadata)

		mockDirectMessaging.Calls = nil // reset call count

		mockDirectMessaging.On(
			"Invoke",
			mock.AnythingOfType("*fasthttp.RequestCtx"),
			"fakeAppID",
			mock.AnythingOfType("*v1.InvokeMethodRequest")).Return(fakeInternalErrorResponse, nil).Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 400, resp.StatusCode)

		// protojson produces different indentation space based on OS
		// For linux
		comp1 := string(resp.RawBody) == "{\"code\":3,\"message\":\"InvalidArgument\",\"details\":[{\"@type\":\"type.googleapis.com/google.rpc.ErrorInfo\",\"reason\":\"fakeReason\"}]}"
		// For mac and windows
		comp2 := string(resp.RawBody) == "{\"code\":3, \"message\":\"InvalidArgument\", \"details\":[{\"@type\":\"type.googleapis.com/google.rpc.ErrorInfo\", \"reason\":\"fakeReason\"}]}"
		assert.True(t, comp1 || comp2)
	})

	t.Run("Invoke direct messaging with malformed status response", func(t *testing.T) {
		malformedDetails := &anypb.Any{TypeUrl: "malformed"}
		fakeInternalErrorResponse := invokev1.NewInvokeMethodResponse(int32(codes.Internal), "InternalError", []*anypb.Any{malformedDetails})
		apiPath := "v1.0/invoke/fakeAppID/method/fakeMethod"
		fakeData := []byte("fakeData")

		fakeReq := invokev1.NewInvokeMethodRequest("fakeMethod")
		fakeReq.WithHTTPExtension(gohttp.MethodPost, "")
		fakeReq.WithRawData(fakeData, "application/json")
		fakeReq.WithMetadata(headerMetadata)

		mockDirectMessaging.Calls = nil // reset call count

		mockDirectMessaging.On(
			"Invoke",
			mock.AnythingOfType("*fasthttp.RequestCtx"),
			"fakeAppID",
			mock.AnythingOfType("*v1.InvokeMethodRequest")).Return(fakeInternalErrorResponse, nil).Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 500, resp.StatusCode)
		assert.True(t, strings.HasPrefix(string(resp.RawBody), "{\"errorCode\":\"ERR_MALFORMED_RESPONSE\",\"message\":\""))
	})

	t.Run("Invoke direct messaging with querystring - 200 OK", func(t *testing.T) {
		apiPath := "v1.0/invoke/fakeAppID/method/fakeMethod?param1=val1&param2=val2"
		fakeData := []byte("fakeData")

		fakeReq := invokev1.NewInvokeMethodRequest("fakeMethod")
		fakeReq.WithHTTPExtension(gohttp.MethodPost, "param1=val1&param2=val2")
		fakeReq.WithRawData(fakeData, "application/json")
		fakeReq.WithMetadata(headerMetadata)

		mockDirectMessaging.Calls = nil // reset call count

		mockDirectMessaging.On("Invoke",
			mock.MatchedBy(func(a context.Context) bool {
				return true
			}), mock.MatchedBy(func(b string) bool {
				return b == "fakeAppID"
			}), mock.MatchedBy(func(c *invokev1.InvokeMethodRequest) bool {
				return true
			})).Return(fakeDirectMessageResponse, nil).Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, []byte("fakeDirectMessageResponse"), resp.RawBody)
	})

	t.Run("Invoke direct messaging - HEAD - 200 OK", func(t *testing.T) {
		apiPath := "v1.0/invoke/fakeAppID/method/fakeMethod?param1=val1&param2=val2"

		fakeReq := invokev1.NewInvokeMethodRequest("fakeMethod")
		fakeReq.WithHTTPExtension(gohttp.MethodHead, "")
		fakeReq.WithMetadata(headerMetadata)

		mockDirectMessaging.Calls = nil // reset call count

		mockDirectMessaging.On("Invoke",
			mock.MatchedBy(func(a context.Context) bool {
				return true
			}), mock.MatchedBy(func(b string) bool {
				return b == "fakeAppID"
			}), mock.MatchedBy(func(c *invokev1.InvokeMethodRequest) bool {
				return true
			})).Return(fakeDirectMessageResponse, nil).Once()

		// act
		resp := fakeServer.DoRequest("HEAD", apiPath, nil, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, []byte{}, resp.RawBody) // Empty body for HEAD
	})

	t.Run("Invoke direct messaging route '/' - 200 OK", func(t *testing.T) {
		apiPath := "v1.0/invoke/fakeAppID/method/"

		fakeReq := invokev1.NewInvokeMethodRequest("/")
		fakeReq.WithHTTPExtension(gohttp.MethodGet, "")
		fakeReq.WithMetadata(headerMetadata)

		mockDirectMessaging.Calls = nil // reset call count

		mockDirectMessaging.On("Invoke",
			mock.MatchedBy(func(a context.Context) bool {
				return true
			}), mock.MatchedBy(func(b string) bool {
				return b == "fakeAppID"
			}), mock.MatchedBy(func(c *invokev1.InvokeMethodRequest) bool {
				return true
			})).Return(fakeDirectMessageResponse, nil).Once()

		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, []byte("fakeDirectMessageResponse"), resp.RawBody)
	})

	t.Run("Invoke returns error - 500 ERR_DIRECT_INVOKE", func(t *testing.T) {
		apiPath := "v1.0/invoke/fakeAppID/method/fakeMethod?param1=val1&param2=val2"
		fakeData := []byte("fakeData")

		fakeReq := invokev1.NewInvokeMethodRequest("fakeMethod")
		fakeReq.WithHTTPExtension(gohttp.MethodPost, "param1=val1&param2=val2")
		fakeReq.WithRawData(fakeData, "application/json")
		fakeReq.WithMetadata(headerMetadata)

		mockDirectMessaging.Calls = nil // reset call count

		mockDirectMessaging.On("Invoke",
			mock.MatchedBy(func(a context.Context) bool {
				return true
			}), mock.MatchedBy(func(b string) bool {
				return b == "fakeAppID"
			}), mock.MatchedBy(func(c *invokev1.InvokeMethodRequest) bool {
				return true
			})).Return(nil, errors.New("UPSTREAM_ERROR")).Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 500, resp.StatusCode)
		assert.Equal(t, "ERR_DIRECT_INVOKE", resp.ErrorBody["errorCode"])
	})

	t.Run("Invoke returns error - 403 ERR_DIRECT_INVOKE", func(t *testing.T) {
		apiPath := "v1.0/invoke/fakeAppID/method/fakeMethod?param1=val1&param2=val2"
		fakeData := []byte("fakeData")

		fakeReq := invokev1.NewInvokeMethodRequest("fakeMethod")
		fakeReq.WithHTTPExtension(gohttp.MethodPost, "param1=val1&param2=val2")
		fakeReq.WithRawData(fakeData, "application/json")
		fakeReq.WithMetadata(headerMetadata)

		mockDirectMessaging.Calls = nil // reset call count

		mockDirectMessaging.On("Invoke",
			mock.MatchedBy(func(a context.Context) bool {
				return true
			}), mock.MatchedBy(func(b string) bool {
				return b == "fakeAppID"
			}), mock.MatchedBy(func(c *invokev1.InvokeMethodRequest) bool {
				return true
			})).Return(nil, status.Errorf(codes.PermissionDenied, "Permission Denied")).Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 403, resp.StatusCode)
		assert.Equal(t, "ERR_DIRECT_INVOKE", resp.ErrorBody["errorCode"])
	})

	fakeServer.Shutdown()
}

func TestV1DirectMessagingEndpointsWithTracer(t *testing.T) {
	headerMetadata := map[string][]string{
		"Accept-Encoding":  {"gzip"},
		"Content-Length":   {"8"},
		"Content-Type":     {"application/json"},
		"Host":             {"localhost"},
		"User-Agent":       {"Go-http-client/1.1"},
		"X-Correlation-Id": {"fake-correlation-id"},
	}
	fakeDirectMessageResponse := invokev1.NewInvokeMethodResponse(200, "OK", nil)
	fakeDirectMessageResponse.WithRawData([]byte("fakeDirectMessageResponse"), "application/json")

	mockDirectMessaging := new(daprt.MockDirectMessaging)

	fakeServer := newFakeHTTPServer()

	buffer := ""
	spec := config.TracingSpec{SamplingRate: "1"}

	createExporters(&buffer)

	testAPI := &api{
		directMessaging: mockDirectMessaging,
		tracingSpec:     spec,
	}
	fakeServer.StartServerWithTracing(spec, testAPI.constructDirectMessagingEndpoints())

	t.Run("Invoke direct messaging without querystring - 200 OK", func(t *testing.T) {
		buffer = ""
		apiPath := "v1.0/invoke/fakeAppID/method/fakeMethod"
		fakeData := []byte("fakeData")

		fakeReq := invokev1.NewInvokeMethodRequest("fakeMethod")
		fakeReq.WithHTTPExtension(gohttp.MethodPost, "")
		fakeReq.WithRawData(fakeData, "application/json")
		fakeReq.WithMetadata(headerMetadata)

		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On("Invoke",
			mock.MatchedBy(func(a context.Context) bool {
				return true
			}), mock.MatchedBy(func(b string) bool {
				return b == "fakeAppID"
			}), mock.MatchedBy(func(c *invokev1.InvokeMethodRequest) bool {
				return true
			})).Return(fakeDirectMessageResponse, nil).Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 200, resp.StatusCode)
	})

	t.Run("Invoke direct messaging with dapr-app-id - 200 OK", func(t *testing.T) {
		buffer = ""
		apiPath := "fakeMethod"
		fakeData := []byte("fakeData")

		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On("Invoke",
			mock.MatchedBy(func(a context.Context) bool {
				return true
			}), mock.MatchedBy(func(b string) bool {
				return b == "fakeAppID"
			}), mock.MatchedBy(func(c *invokev1.InvokeMethodRequest) bool {
				return true
			})).Return(fakeDirectMessageResponse, nil).Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil, "dapr-app-id", "fakeAppID")

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 200, resp.StatusCode)
	})

	t.Run("Invoke direct messaging with querystring - 200 OK", func(t *testing.T) {
		buffer = ""
		apiPath := "v1.0/invoke/fakeAppID/method/fakeMethod?param1=val1&param2=val2"
		fakeData := []byte("fakeData")

		fakeReq := invokev1.NewInvokeMethodRequest("fakeMethod")
		fakeReq.WithHTTPExtension(gohttp.MethodPost, "param1=val1&param2=val2")
		fakeReq.WithRawData(fakeData, "application/json")
		fakeReq.WithMetadata(headerMetadata)

		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On("Invoke",
			mock.MatchedBy(func(a context.Context) bool {
				return true
			}), mock.MatchedBy(func(b string) bool {
				return b == "fakeAppID"
			}), mock.MatchedBy(func(c *invokev1.InvokeMethodRequest) bool {
				return true
			})).Return(fakeDirectMessageResponse, nil).Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 200, resp.StatusCode)
	})

	fakeServer.Shutdown()
}

func TestV1ActorEndpoints(t *testing.T) {
	fakeServer := newFakeHTTPServer()
	testAPI := &api{
		actor: nil,
		json:  jsoniter.ConfigFastest,
	}

	fakeServer.StartServer(testAPI.constructActorEndpoints())

	fakeBodyObject := map[string]interface{}{"data": "fakeData"}
	fakeData, _ := json.Marshal(fakeBodyObject)

	t.Run("Actor runtime is not initialized", func(t *testing.T) {
		apisAndMethods := map[string][]string{
			"v1.0/actors/fakeActorType/fakeActorID/state/key1":          {"GET"},
			"v1.0/actors/fakeActorType/fakeActorID/state":               {"POST", "PUT"},
			"v1.0/actors/fakeActorType/fakeActorID/reminders/reminder1": {"POST", "PUT", "GET", "DELETE"},
			"v1.0/actors/fakeActorType/fakeActorID/method/method1":      {"POST", "PUT", "GET", "DELETE"},
			"v1.0/actors/fakeActorType/fakeActorID/timers/timer1":       {"POST", "PUT", "DELETE"},
		}
		testAPI.actor = nil

		for apiPath, testMethods := range apisAndMethods {
			for _, method := range testMethods {
				// act
				resp := fakeServer.DoRequest(method, apiPath, fakeData, nil)

				// assert
				assert.Equal(t, 500, resp.StatusCode, apiPath)
				assert.Equal(t, "ERR_ACTOR_RUNTIME_NOT_FOUND", resp.ErrorBody["errorCode"])
			}
		}
	})

	t.Run("All PUT/POST APIs - 400 for invalid JSON", func(t *testing.T) {
		testAPI.actor = new(daprt.MockActors)
		apiPaths := []string{
			"v1.0/actors/fakeActorType/fakeActorID/reminders/reminder1",
			"v1.0/actors/fakeActorType/fakeActorID/state",
			"v1.0/actors/fakeActorType/fakeActorID/timers/timer1",
		}

		for _, apiPath := range apiPaths {
			for _, requestMethod := range []string{"PUT", "POST"} {
				// {{
				inputBodyBytes := invalidJSON

				// act
				resp := fakeServer.DoRequest(requestMethod, apiPath, inputBodyBytes, nil)

				// assert
				assert.Equal(t, 400, resp.StatusCode, apiPath)
				assert.Equal(t, "ERR_MALFORMED_REQUEST", resp.ErrorBody["errorCode"])
			}
		}
	})

	t.Run("Get actor state - 200 OK", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state/key1"
		mockActors := new(daprt.MockActors)
		mockActors.On("GetState", &actors.GetStateRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
			Key:       "key1",
		}).Return(&actors.StateResponse{
			Data: fakeData,
		}, nil)

		mockActors.On("IsActorHosted", &actors.ActorHostedRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
		}).Return(true)

		testAPI.actor = mockActors

		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)

		// assert
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, fakeData, resp.RawBody)
		mockActors.AssertNumberOfCalls(t, "GetState", 1)
	})

	t.Run("Get actor state - 204 No Content", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state/key1"
		mockActors := new(daprt.MockActors)
		mockActors.On("GetState", &actors.GetStateRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
			Key:       "key1",
		}).Return(nil, nil)

		mockActors.On("IsActorHosted", &actors.ActorHostedRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
		}).Return(true)

		testAPI.actor = mockActors

		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)

		// assert
		assert.Equal(t, 204, resp.StatusCode)
		assert.Equal(t, []byte{}, resp.RawBody)
		mockActors.AssertNumberOfCalls(t, "GetState", 1)
	})

	t.Run("Get actor state - 500 on GetState failure", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state/key1"
		mockActors := new(daprt.MockActors)
		mockActors.On("GetState", &actors.GetStateRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
			Key:       "key1",
		}).Return(nil, errors.New("UPSTREAM_ERROR"))

		mockActors.On("IsActorHosted", &actors.ActorHostedRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
		}).Return(true)

		testAPI.actor = mockActors

		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)

		// assert
		assert.Equal(t, 500, resp.StatusCode)
		assert.Equal(t, "ERR_ACTOR_STATE_GET", resp.ErrorBody["errorCode"])
		mockActors.AssertNumberOfCalls(t, "GetState", 1)
	})

	t.Run("Get actor state - 400 for missing actor instace", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state/key1"
		mockActors := new(daprt.MockActors)
		mockActors.On("GetState", &actors.GetStateRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
			Key:       "key1",
		}).Return(&actors.StateResponse{
			Data: fakeData,
		}, nil)

		mockActors.On("IsActorHosted", &actors.ActorHostedRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
		}).Return(func(*actors.ActorHostedRequest) bool { return false })

		testAPI.actor = mockActors

		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)

		// assert
		assert.Equal(t, 400, resp.StatusCode)
		mockActors.AssertNumberOfCalls(t, "IsActorHosted", 1)
		assert.Equal(t, "ERR_ACTOR_INSTANCE_MISSING", resp.ErrorBody["errorCode"])
	})

	t.Run("Transaction - 204 No Content", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state"

		testTransactionalOperations := []actors.TransactionalOperation{
			{
				Operation: actors.Upsert,
				Request: map[string]interface{}{
					"key":   "fakeKey1",
					"value": fakeBodyObject,
				},
			},
			{
				Operation: actors.Delete,
				Request: map[string]interface{}{
					"key": "fakeKey1",
				},
			},
		}

		mockActors := new(daprt.MockActors)
		mockActors.On("TransactionalStateOperation", &actors.TransactionalRequest{
			ActorID:    "fakeActorID",
			ActorType:  "fakeActorType",
			Operations: testTransactionalOperations,
		}).Return(nil)

		mockActors.On("IsActorHosted", &actors.ActorHostedRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
		}).Return(true)

		testAPI.actor = mockActors

		// act
		inputBodyBytes, err := json.Marshal(testTransactionalOperations)

		assert.NoError(t, err)
		resp := fakeServer.DoRequest("POST", apiPath, inputBodyBytes, nil)

		// assert
		assert.Equal(t, 204, resp.StatusCode)
		assert.Equal(t, []byte{}, resp.RawBody, "Always give empty body with 204")
		mockActors.AssertNumberOfCalls(t, "TransactionalStateOperation", 1)
		mockActors.AssertNumberOfCalls(t, "IsActorHosted", 1)
	})

	t.Run("Transaction - 400 when actor instance not present", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state"

		testTransactionalOperations := []actors.TransactionalOperation{
			{
				Operation: actors.Upsert,
				Request: map[string]interface{}{
					"key":   "fakeKey1",
					"value": fakeBodyObject,
				},
			},
			{
				Operation: actors.Delete,
				Request: map[string]interface{}{
					"key": "fakeKey1",
				},
			},
		}

		mockActors := new(daprt.MockActors)
		mockActors.On("IsActorHosted", &actors.ActorHostedRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
		}).Return(func(*actors.ActorHostedRequest) bool { return false })

		testAPI.actor = mockActors

		// act
		inputBodyBytes, err := json.Marshal(testTransactionalOperations)

		assert.NoError(t, err)
		resp := fakeServer.DoRequest("POST", apiPath, inputBodyBytes, nil)

		// assert
		assert.Equal(t, 400, resp.StatusCode)
		mockActors.AssertNumberOfCalls(t, "IsActorHosted", 1)
		assert.Equal(t, "ERR_ACTOR_INSTANCE_MISSING", resp.ErrorBody["errorCode"])
	})

	t.Run("Transaction - 500 when transactional state operation fails", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state"

		testTransactionalOperations := []actors.TransactionalOperation{
			{
				Operation: actors.Upsert,
				Request: map[string]interface{}{
					"key":   "fakeKey1",
					"value": fakeBodyObject,
				},
			},
			{
				Operation: actors.Delete,
				Request: map[string]interface{}{
					"key": "fakeKey1",
				},
			},
		}

		mockActors := new(daprt.MockActors)
		mockActors.On("TransactionalStateOperation", &actors.TransactionalRequest{
			ActorID:    "fakeActorID",
			ActorType:  "fakeActorType",
			Operations: testTransactionalOperations,
		}).Return(errors.New("UPSTREAM_ERROR"))

		mockActors.On("IsActorHosted", &actors.ActorHostedRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
		}).Return(true)

		testAPI.actor = mockActors

		// act
		inputBodyBytes, err := json.Marshal(testTransactionalOperations)

		assert.NoError(t, err)
		resp := fakeServer.DoRequest("POST", apiPath, inputBodyBytes, nil)

		// assert
		assert.Equal(t, 500, resp.StatusCode)
		mockActors.AssertNumberOfCalls(t, "TransactionalStateOperation", 1)
		mockActors.AssertNumberOfCalls(t, "IsActorHosted", 1)
		assert.Equal(t, "ERR_ACTOR_STATE_TRANSACTION_SAVE", resp.ErrorBody["errorCode"])
	})

	t.Run("Reminder Create - 204 No Content", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/reminders/reminder1"

		reminderRequest := actors.CreateReminderRequest{
			Name:      "reminder1",
			ActorType: "fakeActorType",
			ActorID:   "fakeActorID",
			Data:      nil,
			DueTime:   "0h0m3s0ms",
			Period:    "0h0m7s0ms",
		}
		mockActors := new(daprt.MockActors)

		mockActors.On("CreateReminder", &reminderRequest).Return(nil)

		testAPI.actor = mockActors

		// act
		inputBodyBytes, err := json.Marshal(reminderRequest)

		assert.NoError(t, err)
		for _, method := range []string{"POST", "PUT"} {
			resp := fakeServer.DoRequest(method, apiPath, inputBodyBytes, nil)
			assert.Equal(t, 204, resp.StatusCode)
		}

		// assert
		mockActors.AssertNumberOfCalls(t, "CreateReminder", 2)
	})

	t.Run("Reminder Create - 500 when CreateReminderFails", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/reminders/reminder1"

		reminderRequest := actors.CreateReminderRequest{
			Name:      "reminder1",
			ActorType: "fakeActorType",
			ActorID:   "fakeActorID",
			Data:      nil,
			DueTime:   "0h0m3s0ms",
			Period:    "0h0m7s0ms",
		}
		mockActors := new(daprt.MockActors)

		mockActors.On("CreateReminder", &reminderRequest).Return(errors.New("UPSTREAM_ERROR"))

		testAPI.actor = mockActors

		// act
		inputBodyBytes, err := json.Marshal(reminderRequest)

		assert.NoError(t, err)
		resp := fakeServer.DoRequest("POST", apiPath, inputBodyBytes, nil)

		// assert
		assert.Equal(t, 500, resp.StatusCode)
		assert.Equal(t, "ERR_ACTOR_REMINDER_CREATE", resp.ErrorBody["errorCode"])
		mockActors.AssertNumberOfCalls(t, "CreateReminder", 1)
	})

	t.Run("Reminder Delete - 204 No Content", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/reminders/reminder1"
		reminderRequest := actors.DeleteReminderRequest{
			Name:      "reminder1",
			ActorType: "fakeActorType",
			ActorID:   "fakeActorID",
		}

		mockActors := new(daprt.MockActors)

		mockActors.On("DeleteReminder", &reminderRequest).Return(nil)

		testAPI.actor = mockActors

		// act
		resp := fakeServer.DoRequest("DELETE", apiPath, nil, nil)

		// assert
		assert.Equal(t, 204, resp.StatusCode)
		assert.Equal(t, []byte{}, resp.RawBody, "Always give empty body with 204")
		mockActors.AssertNumberOfCalls(t, "DeleteReminder", 1)
	})

	t.Run("Reminder Delete - 500 on upstream actor error", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/reminders/reminder1"
		reminderRequest := actors.DeleteReminderRequest{
			Name:      "reminder1",
			ActorType: "fakeActorType",
			ActorID:   "fakeActorID",
		}

		mockActors := new(daprt.MockActors)

		mockActors.On("DeleteReminder", &reminderRequest).Return(errors.New("UPSTREAM_ERROR"))

		testAPI.actor = mockActors

		// act
		resp := fakeServer.DoRequest("DELETE", apiPath, nil, nil)

		// assert
		assert.Equal(t, 500, resp.StatusCode)
		assert.Equal(t, "ERR_ACTOR_REMINDER_DELETE", resp.ErrorBody["errorCode"])
		mockActors.AssertNumberOfCalls(t, "DeleteReminder", 1)
	})

	t.Run("Reminder Get - 200 OK", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/reminders/reminder1"
		reminderRequest := actors.GetReminderRequest{
			Name:      "reminder1",
			ActorType: "fakeActorType",
			ActorID:   "fakeActorID",
		}

		mockActors := new(daprt.MockActors)

		mockActors.On("GetReminder", &reminderRequest).Return(nil, nil)

		testAPI.actor = mockActors

		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)

		// assert
		assert.Equal(t, 200, resp.StatusCode)
		mockActors.AssertNumberOfCalls(t, "GetReminder", 1)
	})

	t.Run("Reminder Get - 500 on upstream actor error", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/reminders/reminder1"
		reminderRequest := actors.GetReminderRequest{
			Name:      "reminder1",
			ActorType: "fakeActorType",
			ActorID:   "fakeActorID",
		}

		mockActors := new(daprt.MockActors)

		mockActors.On("GetReminder", &reminderRequest).Return(nil, errors.New("UPSTREAM_ERROR"))

		testAPI.actor = mockActors

		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)

		// assert
		assert.Equal(t, 500, resp.StatusCode)
		assert.Equal(t, "ERR_ACTOR_REMINDER_GET", resp.ErrorBody["errorCode"])
		mockActors.AssertNumberOfCalls(t, "GetReminder", 1)
	})

	t.Run("Reminder Get - 500 on JSON encode failure from actor", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/reminders/reminder1"
		reminderRequest := actors.GetReminderRequest{
			Name:      "reminder1",
			ActorType: "fakeActorType",
			ActorID:   "fakeActorID",
		}

		reminderResponse := actors.Reminder{
			// Functions are not JSON encodable. This will force the error condition
			Data: func() {},
		}

		mockActors := new(daprt.MockActors)

		mockActors.On("GetReminder", &reminderRequest).Return(&reminderResponse, nil)

		testAPI.actor = mockActors

		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)

		// assert
		assert.Equal(t, 500, resp.StatusCode)
		assert.Equal(t, "ERR_ACTOR_REMINDER_GET", resp.ErrorBody["errorCode"])
		mockActors.AssertNumberOfCalls(t, "GetReminder", 1)
	})

	t.Run("Timer Create - 204 No Content", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/timers/timer1"

		timerRequest := actors.CreateTimerRequest{
			Name:      "timer1",
			ActorType: "fakeActorType",
			ActorID:   "fakeActorID",
			Data:      nil,
			DueTime:   "0h0m3s0ms",
			Period:    "0h0m7s0ms",
			Callback:  "",
		}
		mockActors := new(daprt.MockActors)

		mockActors.On("CreateTimer", &timerRequest).Return(nil)

		testAPI.actor = mockActors

		// act
		inputBodyBytes, err := json.Marshal(timerRequest)

		assert.NoError(t, err)
		for _, method := range []string{"POST", "PUT"} {
			resp := fakeServer.DoRequest(method, apiPath, inputBodyBytes, nil)
			assert.Equal(t, 204, resp.StatusCode)
		}

		// assert
		mockActors.AssertNumberOfCalls(t, "CreateTimer", 2)
	})

	t.Run("Timer Create - 500 on upstream error", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/timers/timer1"

		timerRequest := actors.CreateTimerRequest{
			Name:      "timer1",
			ActorType: "fakeActorType",
			ActorID:   "fakeActorID",
			Data:      nil,
			DueTime:   "0h0m3s0ms",
			Period:    "0h0m7s0ms",
		}
		mockActors := new(daprt.MockActors)

		mockActors.On("CreateTimer", &timerRequest).Return(errors.New("UPSTREAM_ERROR"))

		testAPI.actor = mockActors

		// act
		inputBodyBytes, err := json.Marshal(timerRequest)

		assert.NoError(t, err)
		resp := fakeServer.DoRequest("POST", apiPath, inputBodyBytes, nil)
		assert.Equal(t, 500, resp.StatusCode)
		assert.Equal(t, "ERR_ACTOR_TIMER_CREATE", resp.ErrorBody["errorCode"])

		// assert
		mockActors.AssertNumberOfCalls(t, "CreateTimer", 1)
	})

	t.Run("Timer Delete - 204 No Conent", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/timers/timer1"
		timerRequest := actors.DeleteTimerRequest{
			Name:      "timer1",
			ActorType: "fakeActorType",
			ActorID:   "fakeActorID",
		}

		mockActors := new(daprt.MockActors)

		mockActors.On("DeleteTimer", &timerRequest).Return(nil)

		testAPI.actor = mockActors

		// act
		resp := fakeServer.DoRequest("DELETE", apiPath, nil, nil)

		// assert
		assert.Equal(t, 204, resp.StatusCode)
		assert.Equal(t, []byte{}, resp.RawBody, "Always give empty body with 204")
		mockActors.AssertNumberOfCalls(t, "DeleteTimer", 1)
	})

	t.Run("Timer Delete - 500 For upstream error", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/timers/timer1"
		timerRequest := actors.DeleteTimerRequest{
			Name:      "timer1",
			ActorType: "fakeActorType",
			ActorID:   "fakeActorID",
		}

		mockActors := new(daprt.MockActors)

		mockActors.On("DeleteTimer", &timerRequest).Return(errors.New("UPSTREAM_ERROR"))

		testAPI.actor = mockActors

		// act
		resp := fakeServer.DoRequest("DELETE", apiPath, nil, nil)

		// assert
		assert.Equal(t, 500, resp.StatusCode)
		assert.Equal(t, "ERR_ACTOR_TIMER_DELETE", resp.ErrorBody["errorCode"])
		mockActors.AssertNumberOfCalls(t, "DeleteTimer", 1)
	})

	t.Run("Direct Message - Forwards downstream status", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/method/method1"
		headerMetadata := map[string][]string{
			"Accept-Encoding": {"gzip"},
			"Content-Length":  {"8"},
			"Content-Type":    {"application/json"},
			"Host":            {"localhost"},
			"User-Agent":      {"Go-http-client/1.1"},
		}
		mockActors := new(daprt.MockActors)
		invokeRequest := invokev1.NewInvokeMethodRequest("method1")
		invokeRequest.WithActor("fakeActorType", "fakeActorID")
		fakeData := []byte("fakeData")

		invokeRequest.WithHTTPExtension(gohttp.MethodPost, "")
		invokeRequest.WithRawData(fakeData, "application/json")
		invokeRequest.WithMetadata(headerMetadata)
		response := invokev1.NewInvokeMethodResponse(206, "OK", nil)
		mockActors.On("Call", invokeRequest).Return(response, nil)

		testAPI.actor = mockActors

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		assert.Equal(t, 206, resp.StatusCode)
		mockActors.AssertNumberOfCalls(t, "Call", 1)
	})

	t.Run("Direct Message - 500 for actor call failure", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/method/method1"
		headerMetadata := map[string][]string{
			"Accept-Encoding": {"gzip"},
			"Content-Length":  {"8"},
			"Content-Type":    {"application/json"},
			"Host":            {"localhost"},
			"User-Agent":      {"Go-http-client/1.1"},
		}
		mockActors := new(daprt.MockActors)
		invokeRequest := invokev1.NewInvokeMethodRequest("method1")
		invokeRequest.WithActor("fakeActorType", "fakeActorID")
		fakeData := []byte("fakeData")

		invokeRequest.WithHTTPExtension(gohttp.MethodPost, "")
		invokeRequest.WithRawData(fakeData, "application/json")
		invokeRequest.WithMetadata(headerMetadata)
		mockActors.On("Call", invokeRequest).Return(nil, errors.New("UPSTREAM_ERROR"))

		testAPI.actor = mockActors

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		assert.Equal(t, 500, resp.StatusCode)
		assert.Equal(t, "ERR_ACTOR_INVOKE_METHOD", resp.ErrorBody["errorCode"])
		mockActors.AssertNumberOfCalls(t, "Call", 1)
	})

	fakeServer.Shutdown()
}

func TestV1MetadataEndpoint(t *testing.T) {
	fakeServer := newFakeHTTPServer()

	testAPI := &api{
		actor: nil,
		getComponentsFn: func() []components_v1alpha1.Component {
			return []components_v1alpha1.Component{
				{
					ObjectMeta: meta_v1.ObjectMeta{
						Name: "MockComponent1Name",
					},
					Spec: components_v1alpha1.ComponentSpec{
						Type:    "mock.component1Type",
						Version: "v1.0",
						Metadata: []components_v1alpha1.MetadataItem{
							{
								Name: "actorMockComponent1",
								Value: components_v1alpha1.DynamicValue{
									JSON: v1.JSON{Raw: []byte("true")},
								},
							},
						},
					},
				},
				{
					ObjectMeta: meta_v1.ObjectMeta{
						Name: "MockComponent2Name",
					},
					Spec: components_v1alpha1.ComponentSpec{
						Type:    "mock.component2Type",
						Version: "v1.0",
						Metadata: []components_v1alpha1.MetadataItem{
							{
								Name: "actorMockComponent2",
								Value: components_v1alpha1.DynamicValue{
									JSON: v1.JSON{Raw: []byte("true")},
								},
							},
						},
					},
				},
			}
		},
		json: jsoniter.ConfigFastest,
	}

	fakeServer.StartServer(testAPI.constructMetadataEndpoints())

	expectedBody := map[string]interface{}{
		"id":       "xyz",
		"actors":   []map[string]interface{}{{"type": "abcd", "count": 10}, {"type": "xyz", "count": 5}},
		"extended": make(map[string]string),
		"components": []map[string]interface{}{
			{"name": "MockComponent1Name", "type": "mock.component1Type", "version": "v1.0"},
			{"name": "MockComponent2Name", "type": "mock.component2Type", "version": "v1.0"},
		},
	}
	expectedBodyBytes, _ := json.Marshal(expectedBody)

	t.Run("Metadata - 200 OK", func(t *testing.T) {
		apiPath := "v1.0/metadata"
		mockActors := new(daprt.MockActors)

		mockActors.On("GetActiveActorsCount")

		testAPI.id = "xyz"
		testAPI.actor = mockActors

		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)

		assert.Equal(t, 200, resp.StatusCode)
		assert.ElementsMatch(t, expectedBodyBytes, resp.RawBody)
		mockActors.AssertNumberOfCalls(t, "GetActiveActorsCount", 1)
	})

	fakeServer.Shutdown()
}

func createExporters(buffer *string) {
	exporter := testtrace.NewStringExporter(buffer, logger.NewLogger("fakeLogger"))
	exporter.Register("fakeID")
}

func TestV1ActorEndpointsWithTracer(t *testing.T) {
	fakeServer := newFakeHTTPServer()

	buffer := ""
	spec := config.TracingSpec{SamplingRate: "1"}

	createExporters(&buffer)

	testAPI := &api{
		actor:       nil,
		json:        jsoniter.ConfigFastest,
		tracingSpec: spec,
	}

	fakeServer.StartServerWithTracing(spec, testAPI.constructActorEndpoints())

	fakeBodyObject := map[string]interface{}{"data": "fakeData"}
	fakeData, _ := json.Marshal(fakeBodyObject)

	t.Run("Actor runtime is not initialized", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state/key1"
		testAPI.actor = nil

		testMethods := []string{"GET"}

		for _, method := range testMethods {
			buffer = ""
			// act
			resp := fakeServer.DoRequest(method, apiPath, fakeData, nil)

			// assert
			assert.Equal(t, 500, resp.StatusCode, apiPath)
			assert.Equal(t, "ERR_ACTOR_RUNTIME_NOT_FOUND", resp.ErrorBody["errorCode"], apiPath)
		}
	})

	t.Run("Get actor state - 200 OK", func(t *testing.T) {
		buffer = ""
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state/key1"
		mockActors := new(daprt.MockActors)
		mockActors.On("GetState", &actors.GetStateRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
			Key:       "key1",
		}).Return(&actors.StateResponse{
			Data: fakeData,
		}, nil)

		mockActors.On("IsActorHosted", &actors.ActorHostedRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
		}).Return(true)

		testAPI.actor = mockActors

		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)

		// assert
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, fakeData, resp.RawBody)
		mockActors.AssertNumberOfCalls(t, "GetState", 1)
	})

	t.Run("Transaction - 204 No Content", func(t *testing.T) {
		buffer = ""
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state"

		testTransactionalOperations := []actors.TransactionalOperation{
			{
				Operation: actors.Upsert,
				Request: map[string]interface{}{
					"key":   "fakeKey1",
					"value": fakeBodyObject,
				},
			},
			{
				Operation: actors.Delete,
				Request: map[string]interface{}{
					"key": "fakeKey1",
				},
			},
		}

		mockActors := new(daprt.MockActors)
		mockActors.On("TransactionalStateOperation", &actors.TransactionalRequest{
			ActorID:    "fakeActorID",
			ActorType:  "fakeActorType",
			Operations: testTransactionalOperations,
		}).Return(nil)

		mockActors.On("IsActorHosted", &actors.ActorHostedRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
		}).Return(true)

		testAPI.actor = mockActors

		// act
		inputBodyBytes, err := json.Marshal(testTransactionalOperations)

		assert.NoError(t, err)
		resp := fakeServer.DoRequest("POST", apiPath, inputBodyBytes, nil)

		// assert
		assert.Equal(t, 204, resp.StatusCode)
		assert.Equal(t, []byte{}, resp.RawBody, "Always give empty body with 204")
		mockActors.AssertNumberOfCalls(t, "TransactionalStateOperation", 1)
	})

	fakeServer.Shutdown()
}

func TestAPIToken(t *testing.T) {
	token := "1234"

	os.Setenv("DAPR_API_TOKEN", token)
	defer os.Unsetenv("DAPR_API_TOKEN")

	fakeHeaderMetadata := map[string][]string{
		"Accept-Encoding": {"gzip"},
		"Content-Length":  {"8"},
		"Content-Type":    {"application/json"},
		"Host":            {"localhost"},
		"User-Agent":      {"Go-http-client/1.1"},
	}

	fakeDirectMessageResponse := invokev1.NewInvokeMethodResponse(200, "OK", nil)
	fakeDirectMessageResponse.WithRawData([]byte("fakeDirectMessageResponse"), "application/json")

	mockDirectMessaging := new(daprt.MockDirectMessaging)

	fakeServer := newFakeHTTPServer()

	testAPI := &api{
		directMessaging: mockDirectMessaging,
	}
	fakeServer.StartServerWithAPIToken(testAPI.constructDirectMessagingEndpoints())

	t.Run("Invoke direct messaging with token - 200 OK", func(t *testing.T) {
		apiPath := "v1.0/invoke/fakeDaprID/method/fakeMethod"
		fakeData := []byte("fakeData")

		fakeReq := invokev1.NewInvokeMethodRequest("fakeMethod")
		fakeReq.WithHTTPExtension(gohttp.MethodPost, "")
		fakeReq.WithRawData(fakeData, "application/json")
		fakeReq.WithMetadata(fakeHeaderMetadata)

		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On("Invoke",
			mock.MatchedBy(func(a context.Context) bool {
				return true
			}), mock.MatchedBy(func(b string) bool {
				return b == "fakeDaprID"
			}), mock.MatchedBy(func(c *invokev1.InvokeMethodRequest) bool {
				return true
			})).Return(fakeDirectMessageResponse, nil).Once()

		// act
		resp := fakeServer.DoRequestWithAPIToken("POST", apiPath, token, fakeData)
		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		// TODO Check back as how to assert on generated span ID
		// assert.NotEmpty(t, resp.JSONBody, "failed to generate trace context with invoke")
		assert.Equal(t, 200, resp.StatusCode)
	})

	t.Run("Invoke direct messaging empty token - 401", func(t *testing.T) {
		apiPath := "v1.0/invoke/fakeDaprID/method/fakeMethod"
		fakeData := []byte("fakeData")

		fakeReq := invokev1.NewInvokeMethodRequest("fakeMethod")
		fakeReq.WithHTTPExtension(gohttp.MethodPost, "")
		fakeReq.WithRawData(fakeData, "application/json")
		fakeReq.WithMetadata(fakeHeaderMetadata)

		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On("Invoke",
			mock.MatchedBy(func(a context.Context) bool {
				return true
			}), mock.MatchedBy(func(b string) bool {
				return b == "fakeDaprID"
			}), mock.MatchedBy(func(c *invokev1.InvokeMethodRequest) bool {
				return true
			})).Return(fakeDirectMessageResponse, nil).Once()

		// act
		resp := fakeServer.DoRequestWithAPIToken("POST", apiPath, "", fakeData)
		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 0)
		// TODO Check back as how to assert on generated span ID
		// assert.NotEmpty(t, resp.JSONBody, "failed to generate trace context with invoke")
		assert.Equal(t, 401, resp.StatusCode)
	})

	t.Run("Invoke direct messaging token mismatch - 401", func(t *testing.T) {
		apiPath := "v1.0/invoke/fakeDaprID/method/fakeMethod"
		fakeData := []byte("fakeData")

		fakeReq := invokev1.NewInvokeMethodRequest("fakeMethod")
		fakeReq.WithHTTPExtension(gohttp.MethodPost, "")
		fakeReq.WithRawData(fakeData, "application/json")
		fakeReq.WithMetadata(fakeHeaderMetadata)

		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On("Invoke",
			mock.MatchedBy(func(a context.Context) bool {
				return true
			}), mock.MatchedBy(func(b string) bool {
				return b == "fakeDaprID"
			}), mock.MatchedBy(func(c *invokev1.InvokeMethodRequest) bool {
				return true
			})).Return(fakeDirectMessageResponse, nil).Once()

		// act
		resp := fakeServer.DoRequestWithAPIToken("POST", apiPath, "4567", fakeData)
		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 0)
		// TODO Check back as how to assert on generated span ID
		// assert.NotEmpty(t, resp.JSONBody, "failed to generate trace context with invoke")
		assert.Equal(t, 401, resp.StatusCode)
	})

	t.Run("Invoke direct messaging without token - 401", func(t *testing.T) {
		apiPath := "v1.0/invoke/fakeDaprID/method/fakeMethod"
		fakeData := []byte("fakeData")

		fakeReq := invokev1.NewInvokeMethodRequest("fakeMethod")
		fakeReq.WithHTTPExtension(gohttp.MethodPost, "")
		fakeReq.WithRawData(fakeData, "application/json")
		fakeReq.WithMetadata(fakeHeaderMetadata)

		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On("Invoke",
			mock.MatchedBy(func(a context.Context) bool {
				return true
			}), mock.MatchedBy(func(b string) bool {
				return b == "fakeDaprID"
			}), mock.MatchedBy(func(c *invokev1.InvokeMethodRequest) bool {
				return true
			})).Return(fakeDirectMessageResponse, nil).Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)
		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 0)
		// TODO Check back as how to assert on generated span ID
		// assert.NotEmpty(t, resp.JSONBody, "failed to generate trace context with invoke")
		assert.Equal(t, 401, resp.StatusCode)
	})
}

func TestEmptyPipelineWithTracer(t *testing.T) {
	fakeHeaderMetadata := map[string][]string{
		"Accept-Encoding":  {"gzip"},
		"Content-Length":   {"8"},
		"Content-Type":     {"application/json"},
		"Host":             {"localhost"},
		"User-Agent":       {"Go-http-client/1.1"},
		"X-Correlation-Id": {"fake-correlation-id"},
	}

	fakeDirectMessageResponse := invokev1.NewInvokeMethodResponse(200, "OK", nil)
	fakeDirectMessageResponse.WithRawData([]byte("fakeDirectMessageResponse"), "application/json")

	mockDirectMessaging := new(daprt.MockDirectMessaging)

	fakeServer := newFakeHTTPServer()

	buffer := ""
	spec := config.TracingSpec{SamplingRate: "1.0"}
	pipe := http_middleware.Pipeline{}

	createExporters(&buffer)

	testAPI := &api{
		directMessaging: mockDirectMessaging,
		tracingSpec:     spec,
	}
	fakeServer.StartServerWithTracingAndPipeline(spec, pipe, testAPI.constructDirectMessagingEndpoints())

	t.Run("Invoke direct messaging without querystring - 200 OK", func(t *testing.T) {
		apiPath := "v1.0/invoke/fakeDaprID/method/fakeMethod"
		fakeData := []byte("fakeData")

		fakeReq := invokev1.NewInvokeMethodRequest("fakeMethod")
		fakeReq.WithHTTPExtension(gohttp.MethodPost, "")
		fakeReq.WithRawData(fakeData, "application/json")
		fakeReq.WithMetadata(fakeHeaderMetadata)

		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On("Invoke",
			mock.MatchedBy(func(a context.Context) bool {
				return true
			}), mock.MatchedBy(func(b string) bool {
				return b == "fakeDaprID"
			}), mock.MatchedBy(func(c *invokev1.InvokeMethodRequest) bool {
				return true
			})).Return(fakeDirectMessageResponse, nil).Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		// TODO Check back as how to assert on generated span ID
		// assert.NotEmpty(t, resp.JSONBody, "failed to generate trace context with invoke")
		assert.Equal(t, 200, resp.StatusCode)
	})
}

func buildHTTPPineline(spec config.PipelineSpec) http_middleware.Pipeline {
	registry := http_middleware_loader.NewRegistry()
	registry.Register(http_middleware_loader.New("uppercase", func(metadata middleware.Metadata) (http_middleware.Middleware, error) {
		return func(h fasthttp.RequestHandler) fasthttp.RequestHandler {
			return func(ctx *fasthttp.RequestCtx) {
				body := string(ctx.PostBody())
				ctx.Request.SetBody([]byte(strings.ToUpper(body)))
				h(ctx)
			}
		}, nil
	}))
	var handlers []http_middleware.Middleware
	for i := 0; i < len(spec.Handlers); i++ {
		handler, err := registry.Create(spec.Handlers[i].Type, spec.Handlers[i].Version, middleware.Metadata{})
		if err != nil {
			return http_middleware.Pipeline{}
		}
		handlers = append(handlers, handler)
	}
	return http_middleware.Pipeline{Handlers: handlers}
}

func TestSinglePipelineWithTracer(t *testing.T) {
	fakeHeaderMetadata := map[string][]string{
		"Accept-Encoding":  {"gzip"},
		"Content-Length":   {"8"},
		"Content-Type":     {"application/json"},
		"Host":             {"localhost"},
		"User-Agent":       {"Go-http-client/1.1"},
		"X-Correlation-Id": {"fake-correlation-id"},
	}

	fakeDirectMessageResponse := invokev1.NewInvokeMethodResponse(200, "OK", nil)
	fakeDirectMessageResponse.WithRawData([]byte("fakeDirectMessageResponse"), "application/json")

	mockDirectMessaging := new(daprt.MockDirectMessaging)

	fakeServer := newFakeHTTPServer()

	buffer := ""
	spec := config.TracingSpec{SamplingRate: "1.0"}

	pipeline := buildHTTPPineline(config.PipelineSpec{
		Handlers: []config.HandlerSpec{
			{
				Type: "middleware.http.uppercase",
				Name: "middleware.http.uppercase",
			},
		},
	})

	createExporters(&buffer)

	testAPI := &api{
		directMessaging: mockDirectMessaging,
		tracingSpec:     spec,
	}
	fakeServer.StartServerWithTracingAndPipeline(spec, pipeline, testAPI.constructDirectMessagingEndpoints())

	t.Run("Invoke direct messaging without querystring - 200 OK", func(t *testing.T) {
		buffer = ""
		apiPath := "v1.0/invoke/fakeAppID/method/fakeMethod"
		fakeData := []byte("fakeData")

		fakeReq := invokev1.NewInvokeMethodRequest("fakeMethod")
		fakeReq.WithHTTPExtension(gohttp.MethodPost, "")
		fakeReq.WithRawData([]byte("FAKEDATA"), "application/json")
		fakeReq.WithMetadata(fakeHeaderMetadata)

		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On("Invoke",
			mock.MatchedBy(func(a context.Context) bool {
				return true
			}), mock.MatchedBy(func(b string) bool {
				return b == "fakeAppID"
			}), mock.MatchedBy(func(c *invokev1.InvokeMethodRequest) bool {
				return true
			})).Return(fakeDirectMessageResponse, nil).Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 200, resp.StatusCode)
	})
}

func TestSinglePipelineWithNoTracing(t *testing.T) {
	fakeHeaderMetadata := map[string][]string{
		"Accept-Encoding":  {"gzip"},
		"Content-Length":   {"8"},
		"Content-Type":     {"application/json"},
		"Host":             {"localhost"},
		"User-Agent":       {"Go-http-client/1.1"},
		"X-Correlation-Id": {"fake-correlation-id"},
	}

	fakeDirectMessageResponse := invokev1.NewInvokeMethodResponse(200, "OK", nil)
	fakeDirectMessageResponse.WithRawData([]byte("fakeDirectMessageResponse"), "application/json")

	mockDirectMessaging := new(daprt.MockDirectMessaging)

	fakeServer := newFakeHTTPServer()

	buffer := ""
	spec := config.TracingSpec{SamplingRate: "0"}

	pipeline := buildHTTPPineline(config.PipelineSpec{
		Handlers: []config.HandlerSpec{
			{
				Type: "middleware.http.uppercase",
				Name: "middleware.http.uppercase",
			},
		},
	})

	createExporters(&buffer)

	testAPI := &api{
		directMessaging: mockDirectMessaging,
		tracingSpec:     spec,
	}
	fakeServer.StartServerWithTracingAndPipeline(spec, pipeline, testAPI.constructDirectMessagingEndpoints())

	t.Run("Invoke direct messaging without querystring - 200 OK", func(t *testing.T) {
		buffer = ""
		apiPath := "v1.0/invoke/fakeAppID/method/fakeMethod"
		fakeData := []byte("fakeData")

		fakeReq := invokev1.NewInvokeMethodRequest("fakeMethod")
		fakeReq.WithHTTPExtension(gohttp.MethodPost, "")
		fakeReq.WithRawData([]byte("FAKEDATA"), "application/json")
		fakeReq.WithMetadata(fakeHeaderMetadata)

		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On("Invoke",
			mock.MatchedBy(func(a context.Context) bool {
				return true
			}), mock.MatchedBy(func(b string) bool {
				return b == "fakeAppID"
			}), mock.MatchedBy(func(c *invokev1.InvokeMethodRequest) bool {
				return true
			})).Return(fakeDirectMessageResponse, nil).Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, "", buffer, "failed to generate proper traces with invoke")
		assert.Equal(t, 200, resp.StatusCode)
	})
}

// Fake http server and client helpers to simplify endpoints test.
func newFakeHTTPServer() *fakeHTTPServer {
	return &fakeHTTPServer{}
}

type fakeHTTPServer struct {
	ln     *fasthttputil.InmemoryListener
	client gohttp.Client
}

type fakeHTTPResponse struct {
	StatusCode  int
	ContentType string
	RawHeader   gohttp.Header
	RawBody     []byte
	JSONBody    interface{}
	ErrorBody   map[string]string
}

func (f *fakeHTTPServer) StartServer(endpoints []Endpoint) {
	router := f.getRouter(endpoints)
	f.ln = fasthttputil.NewInmemoryListener()
	go func() {
		if err := fasthttp.Serve(f.ln, router.Handler); err != nil {
			panic(fmt.Errorf("failed to serve: %v", err))
		}
	}()

	f.client = gohttp.Client{
		Transport: &gohttp.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return f.ln.Dial()
			},
		},
	}
}

func (f *fakeHTTPServer) StartServerWithTracing(spec config.TracingSpec, endpoints []Endpoint) {
	router := f.getRouter(endpoints)
	f.ln = fasthttputil.NewInmemoryListener()
	go func() {
		if err := fasthttp.Serve(f.ln, diag.HTTPTraceMiddleware(router.Handler, "fakeAppID", spec)); err != nil {
			panic(fmt.Errorf("failed to set tracing span context: %v", err))
		}
	}()

	f.client = gohttp.Client{
		Transport: &gohttp.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return f.ln.Dial()
			},
		},
	}
}

func (f *fakeHTTPServer) StartServerWithAPIToken(endpoints []Endpoint) {
	router := f.getRouter(endpoints)
	f.ln = fasthttputil.NewInmemoryListener()
	go func() {
		if err := fasthttp.Serve(f.ln, useAPIAuthentication(router.Handler)); err != nil {
			panic(fmt.Errorf("failed to serve: %v", err))
		}
	}()

	f.client = gohttp.Client{
		Transport: &gohttp.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return f.ln.Dial()
			},
		},
	}
}

func (f *fakeHTTPServer) StartServerWithTracingAndPipeline(spec config.TracingSpec, pipeline http_middleware.Pipeline, endpoints []Endpoint) {
	router := f.getRouter(endpoints)
	f.ln = fasthttputil.NewInmemoryListener()
	go func() {
		handler := pipeline.Apply(router.Handler)
		if err := fasthttp.Serve(f.ln, diag.HTTPTraceMiddleware(handler, "fakeAppID", spec)); err != nil {
			panic(fmt.Errorf("failed to serve tracing span context: %v", err))
		}
	}()

	f.client = gohttp.Client{
		Transport: &gohttp.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return f.ln.Dial()
			},
		},
	}
}

func (f *fakeHTTPServer) getRouter(endpoints []Endpoint) *routing.Router {
	router := routing.New()

	for _, e := range endpoints {
		path := fmt.Sprintf("/%s/%s", e.Version, e.Route)
		for _, m := range e.Methods {
			router.Handle(m, path, e.Handler)
		}
		if e.Alias != "" {
			path = fmt.Sprintf("/%s", e.Alias)
			for _, m := range e.Methods {
				router.Handle(m, path, e.Handler)
			}
		}
	}
	return router
}

func (f *fakeHTTPServer) Shutdown() {
	f.ln.Close()
}

func (f *fakeHTTPServer) DoRequestWithAPIToken(method, path, token string, body []byte) fakeHTTPResponse {
	url := fmt.Sprintf("http://localhost/%s", path)
	r, _ := gohttp.NewRequest(method, url, bytes.NewBuffer(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("dapr-api-token", token)
	res, err := f.client.Do(r)
	if err != nil {
		panic(fmt.Errorf("failed to request: %v", err))
	}

	res.Body.Close()
	response := fakeHTTPResponse{
		StatusCode:  res.StatusCode,
		ContentType: res.Header.Get("Content-Type"),
		RawHeader:   res.Header,
	}
	return response
}

func (f *fakeHTTPServer) doRequest(basicAuth, method, path string, body []byte, params map[string]string, headers ...string) fakeHTTPResponse {
	url := fmt.Sprintf("http://localhost/%s", path)
	if basicAuth != "" {
		url = fmt.Sprintf("http://%s@localhost/%s", basicAuth, path)
	}

	if params != nil {
		url += "?"
		for k, v := range params {
			url += k + "=" + v + "&"
		}
		url = url[:len(url)-1]
	}
	r, _ := gohttp.NewRequest(method, url, bytes.NewBuffer(body))
	r.Header.Set("Content-Type", "application/json")

	for i := 0; i < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}

	res, err := f.client.Do(r)
	if err != nil {
		panic(fmt.Errorf("failed to request: %v", err))
	}

	bodyBytes, _ := io.ReadAll(res.Body)
	defer res.Body.Close()
	response := fakeHTTPResponse{
		StatusCode:  res.StatusCode,
		ContentType: res.Header.Get("Content-Type"),
		RawHeader:   res.Header,
		RawBody:     bodyBytes,
	}

	if response.ContentType == "application/json" {
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			json.Unmarshal(bodyBytes, &response.JSONBody)
		} else {
			json.Unmarshal(bodyBytes, &response.ErrorBody)
		}
	}

	return response
}

func (f *fakeHTTPServer) DoRequest(method, path string, body []byte, params map[string]string, headers ...string) fakeHTTPResponse {
	return f.doRequest("", method, path, body, params, headers...)
}

func TestV1StateEndpoints(t *testing.T) {
	etag := "`~!@#$%^&*()_+-={}[]|\\:\";'<>?,./'"
	fakeServer := newFakeHTTPServer()
	var fakeStore state.Store = fakeStateStoreQuerier{}
	fakeStores := map[string]state.Store{
		"store1": fakeStore,
	}
	fakeTransactionalStores := map[string]state.TransactionalStore{
		"store1": fakeStore.(state.TransactionalStore),
	}
	testAPI := &api{
		stateStores:              fakeStores,
		transactionalStateStores: fakeTransactionalStores,
		json:                     jsoniter.ConfigFastest,
	}
	fakeServer.StartServer(testAPI.constructStateEndpoints())
	storeName := "store1"

	t.Run("Get state - 400 ERR_STATE_STORE_NOT_FOUND or NOT_CONFIGURED", func(t *testing.T) {
		apisAndMethods := map[string][]string{
			"v1.0/state/nonexistantStore/bad-key":      {"GET", "DELETE"},
			"v1.0/state/nonexistantStore/":             {"POST", "PUT"},
			"v1.0/state/nonexistantStore/bulk":         {"POST", "PUT"},
			"v1.0/state/nonexistantStore/transaction":  {"POST", "PUT"},
			"v1.0-alpha1/state/nonexistantStore/query": {"POST", "PUT"},
		}

		for apiPath, testMethods := range apisAndMethods {
			for _, method := range testMethods {
				testAPI.stateStores = nil
				resp := fakeServer.DoRequest(method, apiPath, nil, nil)
				// assert
				assert.Equal(t, 500, resp.StatusCode, apiPath)
				assert.Equal(t, "ERR_STATE_STORES_NOT_CONFIGURED", resp.ErrorBody["errorCode"])
				testAPI.stateStores = fakeStores

				// act
				resp = fakeServer.DoRequest(method, apiPath, nil, nil)
				// assert
				assert.Equal(t, 400, resp.StatusCode, apiPath)
				assert.Equal(t, "ERR_STATE_STORE_NOT_FOUND", resp.ErrorBody["errorCode"], apiPath)
			}
		}
	})

	t.Run("State PUT/POST APIs - 400 invalid JSON request", func(t *testing.T) {
		apiPaths := []string{
			"v1.0/state/store1/",
			"v1.0/state/store1/bulk",
			"v1.0/state/store1/transaction",
			"v1.0-alpha1/state/store1/query",
		}

		for _, apiPath := range apiPaths {
			for _, requestMethod := range []string{"PUT", "POST"} {
				// {{
				inputBodyBytes := invalidJSON

				// act
				resp := fakeServer.DoRequest(requestMethod, apiPath, inputBodyBytes, nil)

				// assert
				assert.Equal(t, 400, resp.StatusCode, apiPath)
				assert.Equal(t, "ERR_MALFORMED_REQUEST", resp.ErrorBody["errorCode"], apiPath)
			}
		}
	})

	t.Run("Get state - 204 No Content Found", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/bad-key", storeName)
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 204, resp.StatusCode, "reading non-existing key should return 204")
		assert.Equal(t, []byte{}, resp.RawBody, "Always give empty body with 204")
	})

	t.Run("Get state - Good Key", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/good-key", storeName)
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 200, resp.StatusCode, "reading existing key should succeed")
		assert.Equal(t, etag, resp.RawHeader.Get("ETag"), "failed to read etag")
	})

	t.Run("Get state - Upstream error", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/error-key", storeName)
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 500, resp.StatusCode, "reading existing key should succeed")
		assert.Equal(t, "ERR_STATE_GET", resp.ErrorBody["errorCode"])
	})

	t.Run("Update state - PUT verb supported", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s", storeName)
		request := []state.SetRequest{{
			Key: "good-key",
		}}
		b, _ := json.Marshal(request)
		// act
		resp := fakeServer.DoRequest("PUT", apiPath, b, nil)
		// assert
		assert.Equal(t, 204, resp.StatusCode, "updating the state store with the PUT verb should succeed")
		assert.Equal(t, []byte{}, resp.RawBody, "Always give empty body with 204")
	})

	t.Run("Update state - No ETag", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s", storeName)
		request := []state.SetRequest{{
			Key: "good-key",
		}}
		b, _ := json.Marshal(request)
		// act
		resp := fakeServer.DoRequest("POST", apiPath, b, nil)
		// assert
		assert.Equal(t, 204, resp.StatusCode, "updating existing key without etag should succeed")
		assert.Equal(t, []byte{}, resp.RawBody, "Always give empty body with 204")
	})

	t.Run("Update state - State Error", func(t *testing.T) {
		empty := ""
		apiPath := fmt.Sprintf("v1.0/state/%s", storeName)
		request := []state.SetRequest{{
			Key:  "error-key",
			ETag: &empty,
		}}
		b, _ := json.Marshal(request)
		// act
		resp := fakeServer.DoRequest("POST", apiPath, b, nil)
		// assert
		assert.Equal(t, 500, resp.StatusCode, "state error should return 500 status")
		assert.Equal(t, "ERR_STATE_SAVE", resp.ErrorBody["errorCode"])
	})

	t.Run("Update state - Matching ETag", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s", storeName)
		request := []state.SetRequest{{
			Key:  "good-key",
			ETag: &etag,
		}}
		b, _ := json.Marshal(request)
		// act
		resp := fakeServer.DoRequest("POST", apiPath, b, nil)
		// assert
		assert.Equal(t, 204, resp.StatusCode, "updating existing key with matching etag should succeed")
		assert.Equal(t, []byte{}, resp.RawBody, "Always give empty body with 204")
	})

	t.Run("Update state - Wrong ETag", func(t *testing.T) {
		invalidEtag := "BAD ETAG"
		apiPath := fmt.Sprintf("v1.0/state/%s", storeName)
		request := []state.SetRequest{{
			Key:  "good-key",
			ETag: &invalidEtag,
		}}
		b, _ := json.Marshal(request)
		// act
		resp := fakeServer.DoRequest("POST", apiPath, b, nil)
		// assert
		assert.Equal(t, 500, resp.StatusCode, "updating existing key with wrong etag should fail")
	})

	t.Run("Delete state - No ETag", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/good-key", storeName)
		// act
		resp := fakeServer.DoRequest("DELETE", apiPath, nil, nil)
		// assert
		assert.Equal(t, 204, resp.StatusCode, "updating existing key without etag should succeed")
		assert.Equal(t, []byte{}, resp.RawBody, "Always give empty body with 204")
	})

	t.Run("Delete state - Matching ETag", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/good-key", storeName)
		// act
		resp := fakeServer.DoRequest("DELETE", apiPath, nil, nil, "If-Match", etag)
		// assert
		assert.Equal(t, 204, resp.StatusCode, "updating existing key with matching etag should succeed")
		assert.Equal(t, []byte{}, resp.RawBody, "Always give empty body with 204")
	})

	t.Run("Delete state - Bad ETag", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/good-key", storeName)
		// act
		resp := fakeServer.DoRequest("DELETE", apiPath, nil, nil, "If-Match", "BAD ETAG")
		// assert
		assert.Equal(t, 500, resp.StatusCode, "updating existing key with wrong etag should fail")
	})

	t.Run("Bulk state get - Empty request", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/bulk", storeName)
		request := BulkGetRequest{}
		body, _ := json.Marshal(request)
		// act
		resp := fakeServer.DoRequest("POST", apiPath, body, nil)
		// assert
		assert.Equal(t, 200, resp.StatusCode, "Bulk API should succeed on an empty body")
	})

	t.Run("Bulk state get - PUT request", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/bulk", storeName)
		request := BulkGetRequest{}
		body, _ := json.Marshal(request)
		// act
		resp := fakeServer.DoRequest("PUT", apiPath, body, nil)
		// assert
		assert.Equal(t, 200, resp.StatusCode, "Bulk API should succeed on an empty body")
	})

	t.Run("Bulk state get - normal request", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/bulk", storeName)
		request := BulkGetRequest{
			Keys: []string{"good-key", "foo"},
		}
		body, _ := json.Marshal(request)

		// act

		resp := fakeServer.DoRequest("POST", apiPath, body, nil)

		// assert
		assert.Equal(t, 200, resp.StatusCode, "Bulk API should succeed on a normal request")

		var responses []BulkGetResponse

		assert.NoError(t, json.Unmarshal(resp.RawBody, &responses), "Response should be valid JSON")

		expectedResponses := []BulkGetResponse{
			{
				Key:   "good-key",
				Data:  jsoniter.RawMessage("life is good"),
				ETag:  ptr.String("`~!@#$%^&*()_+-={}[]|\\:\";'<>?,./'"),
				Error: "",
			},
			{
				Key:   "foo",
				Data:  nil,
				ETag:  nil,
				Error: "",
			},
		}

		assert.Equal(t, expectedResponses, responses, "Responses do not match")
	})

	t.Run("Bulk state get - one key returns error", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/bulk", storeName)
		request := BulkGetRequest{
			Keys: []string{"good-key", "error-key"},
		}
		body, _ := json.Marshal(request)
		// act
		resp := fakeServer.DoRequest("POST", apiPath, body, nil)
		// assert
		assert.Equal(t, 200, resp.StatusCode, "Bulk API should succeed even if key not found")

		var responses []BulkGetResponse

		assert.NoError(t, json.Unmarshal(resp.RawBody, &responses), "Response should be valid JSON")

		expectedResponses := []BulkGetResponse{
			{
				Key:   "good-key",
				Data:  jsoniter.RawMessage("life is good"),
				ETag:  ptr.String("`~!@#$%^&*()_+-={}[]|\\:\";'<>?,./'"),
				Error: "",
			},
			{
				Key:   "error-key",
				Data:  nil,
				ETag:  nil,
				Error: "UPSTREAM STATE ERROR",
			},
		}

		assert.Equal(t, expectedResponses, responses, "Responses do not match")
	})

	t.Run("Query state request", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0-alpha1/state/%s/query", storeName)
		// act
		resp := fakeServer.DoRequest("PUT", apiPath, []byte(queryTestRequestOK), nil)
		// assert
		assert.Equal(t, 200, resp.StatusCode)
		// act
		resp = fakeServer.DoRequest("POST", apiPath, []byte(queryTestRequestOK), nil)
		// assert
		assert.Equal(t, 200, resp.StatusCode)
		// act
		resp = fakeServer.DoRequest("POST", apiPath, []byte(queryTestRequestNoRes), nil)
		// assert
		assert.Equal(t, 204, resp.StatusCode)
		// act
		resp = fakeServer.DoRequest("POST", apiPath, []byte(queryTestRequestErr), nil)
		// assert
		assert.Equal(t, 500, resp.StatusCode)
		// act
		resp = fakeServer.DoRequest("POST", apiPath, []byte(queryTestRequestSyntaxErr), nil)
		// assert
		assert.Equal(t, 400, resp.StatusCode)
	})
}

func TestStateStoreQuerierNotImplemented(t *testing.T) {
	fakeServer := newFakeHTTPServer()
	testAPI := &api{
		stateStores: map[string]state.Store{"store1": fakeStateStore{}},
	}
	fakeServer.StartServer(testAPI.constructStateEndpoints())

	resp := fakeServer.DoRequest("POST", "v1.0-alpha1/state/store1/query", nil, nil)
	// assert
	assert.Equal(t, 404, resp.StatusCode)
	assert.Equal(t, "ERR_METHOD_NOT_FOUND", resp.ErrorBody["errorCode"])
}

func TestStateStoreQuerierNotEnabled(t *testing.T) {
	fakeServer := newFakeHTTPServer()
	testAPI := &api{
		stateStores: map[string]state.Store{"store1": fakeStateStoreQuerier{}},
	}
	fakeServer.StartServer(testAPI.constructStateEndpoints())

	resp := fakeServer.DoRequest("POST", "v1.0/state/store1/query", nil, nil)
	// assert
	assert.Equal(t, 405, resp.StatusCode)
}

const (
	queryTestRequestOK = `{
	"query": {
		"filter": {
			"EQ": { "a": "b" }
		},
		"sort": [
			{ "key": "a" }
		],
		"pagination": {
			"limit": 2
		}
	}
}`
	queryTestRequestNoRes = `{
	"query": {
		"filter": {
			"EQ": { "a": "b" }
		},
		"pagination": {
			"limit": 2
		}
	}
}`
	queryTestRequestErr = `{
	"query": {
		"filter": {
			"EQ": { "a": "b" }
		},
		"sort": [
			{ "key": "a" }
		]
	}
}`
	queryTestRequestSyntaxErr = `syntax error`
)

type fakeStateStore struct {
	counter int
}

func (c fakeStateStore) Ping() error {
	return nil
}

func (c fakeStateStore) BulkDelete(req []state.DeleteRequest) error {
	for i := range req {
		r := req[i] // Make a copy since we will refer to this as a reference in this loop.
		err := c.Delete(&r)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c fakeStateStore) BulkSet(req []state.SetRequest) error {
	for i := range req {
		s := req[i] // Make a copy since we will refer to this as a reference in this loop.
		err := c.Set(&s)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c fakeStateStore) Delete(req *state.DeleteRequest) error {
	if req.Key == "good-key" {
		if req.ETag != nil && *req.ETag != "`~!@#$%^&*()_+-={}[]|\\:\";'<>?,./'" {
			return errors.New("ETag mismatch")
		}
		return nil
	}
	return errors.New("NOT FOUND")
}

func (c fakeStateStore) Get(req *state.GetRequest) (*state.GetResponse, error) {
	if req.Key == "good-key" {
		return &state.GetResponse{
			Data: []byte("\"bGlmZSBpcyBnb29k\""),
			ETag: ptr.String("`~!@#$%^&*()_+-={}[]|\\:\";'<>?,./'"),
		}, nil
	}
	if req.Key == "error-key" {
		return nil, errors.New("UPSTREAM STATE ERROR")
	}
	return nil, nil
}

// BulkGet performs a bulks get operations.
func (c fakeStateStore) BulkGet(req []state.GetRequest) (bool, []state.BulkGetResponse, error) {
	return false, nil, nil
}

func (c fakeStateStore) Init(metadata state.Metadata) error {
	c.counter = 0
	return nil
}

func (c fakeStateStore) Features() []state.Feature {
	return []state.Feature{
		state.FeatureETag,
		state.FeatureTransactional,
	}
}

func (c fakeStateStore) Set(req *state.SetRequest) error {
	if req.Key == "good-key" {
		if req.ETag != nil && *req.ETag != "`~!@#$%^&*()_+-={}[]|\\:\";'<>?,./'" {
			return errors.New("ETag mismatch")
		}
		return nil
	}
	return errors.New("NOT FOUND")
}

func (c fakeStateStore) Multi(request *state.TransactionalStateRequest) error {
	if request.Metadata != nil && request.Metadata["error"] == "true" {
		return errors.New("Transaction error")
	}
	return nil
}

type fakeStateStoreQuerier struct {
	fakeStateStore
}

func (c fakeStateStoreQuerier) Query(req *state.QueryRequest) (*state.QueryResponse, error) {
	// simulate empty data
	if req.Query.Sort == nil {
		return &state.QueryResponse{}, nil
	}
	// simulate error
	if req.Query.Page.Limit == 0 {
		return nil, errors.New("Query error")
	}
	// simulate full result
	return &state.QueryResponse{
		Results: []state.QueryItem{
			{
				Key:  "1",
				Data: []byte(`{"a":"b"}`),
			},
		},
	}, nil
}

func TestV1SecretEndpoints(t *testing.T) {
	fakeServer := newFakeHTTPServer()
	fakeStore := daprt.FakeSecretStore{}
	fakeStores := map[string]secretstores.SecretStore{
		"store1": fakeStore,
		"store2": fakeStore,
		"store3": fakeStore,
		"store4": fakeStore,
	}
	secretsConfiguration := map[string]config.SecretsScope{
		"store1": {
			DefaultAccess: config.AllowAccess,
			DeniedSecrets: []string{"not-allowed"},
		},
		"store2": {
			DefaultAccess:  config.DenyAccess,
			AllowedSecrets: []string{"good-key"},
		},
		"store3": {
			DefaultAccess:  config.AllowAccess,
			AllowedSecrets: []string{"good-key"},
		},
	}

	testAPI := &api{
		secretsConfiguration: secretsConfiguration,
		secretStores:         fakeStores,
		json:                 jsoniter.ConfigFastest,
	}
	fakeServer.StartServer(testAPI.constructSecretEndpoints())
	storeName := "store1"
	deniedStoreName := "store2"
	restrictedStore := "store3"
	unrestrictedStore := "store4" // No configuration defined for the store

	t.Run("Get secret- 401 ERR_SECRET_STORE_NOT_FOUND", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/secrets/%s/bad-key", "notexistStore")
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 401, resp.StatusCode, "reading non-existing store should return 401")
	})

	t.Run("Get secret - 204 No Content Found", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/secrets/%s/bad-key", storeName)
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 204, resp.StatusCode, "reading non-existing key should return 204")
		assert.Equal(t, []byte{}, resp.RawBody, "Always give empty body with 204")
	})

	t.Run("Get secret - 403 Permission denied ", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/secrets/%s/not-allowed", storeName)
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 403, resp.StatusCode, "reading not allowed key should return 403")
	})

	t.Run("Get secret - 403 Permission denied ", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/secrets/%s/random", deniedStoreName)
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 403, resp.StatusCode, "reading random key from store with default deny access should return 403")
	})

	t.Run("Get secret - 403 Permission denied ", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/secrets/%s/random", restrictedStore)
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 403, resp.StatusCode, "reading random key from store with restricted allow access should return 403")
	})

	t.Run("Get secret - 200 Good Ket restricted store ", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/secrets/%s/good-key", restrictedStore)
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 200, resp.StatusCode, "reading good-key key from store with restricted allow access should return 200")
	})

	t.Run("Get secret - 200 Good Key allowed access ", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/secrets/%s/good-key", deniedStoreName)
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 200, resp.StatusCode, "reading allowed good-key key from store with default deny access should return 200")
	})

	t.Run("Get secret - Good Key default allow", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/secrets/%s/good-key", storeName)
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 200, resp.StatusCode, "reading existing key should succeed")
	})

	t.Run("Get secret - Good Key from unrestricted store", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/secrets/%s/good-key", unrestrictedStore)
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 200, resp.StatusCode, "reading existing key should succeed")
	})

	t.Run("Get secret - 500 for upstream error", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/secrets/%s/error-key", unrestrictedStore)
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 500, resp.StatusCode, "reading existing key should succeed")
		assert.Equal(t, "ERR_SECRET_GET", resp.ErrorBody["errorCode"], apiPath)
	})

	t.Run("Get secret - 500 for secret store not congfigured", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/secrets/%s/good-key", unrestrictedStore)
		// act
		testAPI.secretStores = nil

		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 500, resp.StatusCode, "reading existing key should succeed")
		assert.Equal(t, "ERR_SECRET_STORES_NOT_CONFIGURED", resp.ErrorBody["errorCode"], apiPath)

		testAPI.secretStores = fakeStores
	})

	t.Run("Get Bulk secret - Good Key default allow", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/secrets/%s/bulk", storeName)
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 200, resp.StatusCode, "reading secrets should succeed")
	})
}

func TestV1HealthzEndpoint(t *testing.T) {
	fakeServer := newFakeHTTPServer()

	testAPI := &api{
		actor: nil,
		json:  jsoniter.ConfigFastest,
	}

	fakeServer.StartServer(testAPI.constructHealthzEndpoints())

	t.Run("Healthz - 500 ERR_HEALTH_NOT_READY", func(t *testing.T) {
		apiPath := "v1.0/healthz"
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)

		assert.Equal(t, 500, resp.StatusCode, "dapr not ready should return 500")
	})

	t.Run("Healthz - 204 No Content", func(t *testing.T) {
		apiPath := "v1.0/healthz"
		testAPI.MarkStatusAsReady()
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)

		assert.Equal(t, 204, resp.StatusCode)
	})

	fakeServer.Shutdown()
}

func TestV1TransactionEndpoints(t *testing.T) {
	fakeServer := newFakeHTTPServer()
	var fakeStore state.Store = fakeStateStoreQuerier{}
	fakeStoreNonTransactional := new(daprt.MockStateStore)
	fakeStores := map[string]state.Store{
		"store1":                fakeStore,
		"storeNonTransactional": fakeStoreNonTransactional,
	}
	fakeTransactionalStores := map[string]state.TransactionalStore{
		"store1": fakeStore.(state.TransactionalStore),
	}
	testAPI := &api{
		stateStores:              fakeStores,
		transactionalStateStores: fakeTransactionalStores,
		json:                     jsoniter.ConfigFastest,
	}
	fakeServer.StartServer(testAPI.constructStateEndpoints())
	fakeBodyObject := map[string]interface{}{"data": "fakeData"}
	storeName := "store1"
	nonTransactionalStoreName := "storeNonTransactional"

	t.Run("Direct Transaction - 204 No Content", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/transaction", storeName)
		testTransactionalOperations := []state.TransactionalStateOperation{
			{
				Operation: state.Upsert,
				Request: map[string]interface{}{
					"key":   "fakeKey1",
					"value": fakeBodyObject,
				},
			},
			{
				Operation: state.Delete,
				Request: map[string]interface{}{
					"key": "fakeKey1",
				},
			},
		}

		// act
		inputBodyBytes, err := json.Marshal(state.TransactionalStateRequest{
			Operations: testTransactionalOperations,
		})

		assert.NoError(t, err)
		resp := fakeServer.DoRequest("POST", apiPath, inputBodyBytes, nil)

		// assert
		assert.Equal(t, 204, resp.StatusCode, "Dapr should return 204")
		assert.Equal(t, []byte{}, resp.RawBody, "Always give empty body with 204")
	})

	t.Run("Post non-existent state store - 400 No State Store Found", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/transaction", "non-existent-store")
		testTransactionalOperations := []state.TransactionalStateOperation{
			{
				Operation: state.Upsert,
				Request: map[string]interface{}{
					"key":   "fakeKey1",
					"value": fakeBodyObject,
				},
			},
			{
				Operation: state.Delete,
				Request: map[string]interface{}{
					"key": "fakeKey1",
				},
			},
		}

		// act
		inputBodyBytes, err := json.Marshal(state.TransactionalStateRequest{
			Operations: testTransactionalOperations,
		})
		assert.NoError(t, err)
		resp := fakeServer.DoRequest("POST", apiPath, inputBodyBytes, nil)
		// assert
		assert.Equal(t, 400, resp.StatusCode, "Accessing non-existent state store should return 400")
	})

	t.Run("Invalid opperation - 400 ERR_NOT_SUPPORTED_STATE_OPERATION", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/transaction", storeName)
		testTransactionalOperations := []state.TransactionalStateOperation{
			{
				Operation: "foo",
				Request: map[string]interface{}{
					"key":   "fakeKey1",
					"value": fakeBodyObject,
				},
			},
		}

		// act
		inputBodyBytes, err := json.Marshal(state.TransactionalStateRequest{
			Operations: testTransactionalOperations,
		})

		assert.NoError(t, err)
		resp := fakeServer.DoRequest("POST", apiPath, inputBodyBytes, nil)

		// assert
		assert.Equal(t, 400, resp.StatusCode, "Dapr should return 400")
		assert.Equal(t, "ERR_NOT_SUPPORTED_STATE_OPERATION", resp.ErrorBody["errorCode"], apiPath)
	})

	t.Run("Invalid request obj - 400 ERR_MALFORMED_REQUEST", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/transaction", storeName)
		for _, operation := range []state.OperationType{state.Upsert, state.Delete} {
			testTransactionalOperations := []state.TransactionalStateOperation{
				{
					Operation: operation,
					Request: map[string]interface{}{
						// Should cause the decorder to fail
						"key":   []string{"fakeKey1"},
						"value": fakeBodyObject,
					},
				},
			}

			// act
			inputBodyBytes, err := json.Marshal(state.TransactionalStateRequest{
				Operations: testTransactionalOperations,
			})

			assert.NoError(t, err)
			resp := fakeServer.DoRequest("POST", apiPath, inputBodyBytes, nil)

			// assert
			assert.Equal(t, 400, resp.StatusCode, "Dapr should return 400")
			assert.Equal(t, "ERR_MALFORMED_REQUEST", resp.ErrorBody["errorCode"], apiPath)
		}
	})

	t.Run("Non Transactional State Store - 500 ERR_STATE_STORE_NOT_SUPPORTED", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/transaction", nonTransactionalStoreName)
		testTransactionalOperations := []state.TransactionalStateOperation{
			{
				Operation: state.Upsert,
				Request: map[string]interface{}{
					"key":   "fakeKey1",
					"value": fakeBodyObject,
				},
			},
		}

		// act
		inputBodyBytes, err := json.Marshal(state.TransactionalStateRequest{
			Operations: testTransactionalOperations,
		})

		assert.NoError(t, err)
		resp := fakeServer.DoRequest("POST", apiPath, inputBodyBytes, nil)

		// assert
		assert.Equal(t, 500, resp.StatusCode, "Dapr should return 500")
		assert.Equal(t, "ERR_STATE_STORE_NOT_SUPPORTED", resp.ErrorBody["errorCode"], apiPath)
	})

	t.Run("Direct Transaction upstream failure - 500 ERR_STATE_TRANSACTION", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/transaction", storeName)
		testTransactionalOperations := []state.TransactionalStateOperation{
			{
				Operation: state.Upsert,
				Request: map[string]interface{}{
					"key":   "fakeKey1",
					"value": fakeBodyObject,
				},
			},
			{
				Operation: state.Delete,
				Request: map[string]interface{}{
					"key": "fakeKey1",
				},
			},
		}

		// act
		inputBodyBytes, err := json.Marshal(state.TransactionalStateRequest{
			Operations: testTransactionalOperations,
			Metadata: map[string]string{
				"error": "true",
			},
		})

		assert.NoError(t, err)
		resp := fakeServer.DoRequest("POST", apiPath, inputBodyBytes, nil)

		// assert
		assert.Equal(t, 500, resp.StatusCode, "Dapr should return 500")
		assert.Equal(t, "ERR_STATE_TRANSACTION", resp.ErrorBody["errorCode"], apiPath)
	})
	fakeServer.Shutdown()
}

func TestStateStoreErrors(t *testing.T) {
	t.Run("non etag error", func(t *testing.T) {
		a := &api{}
		err := errors.New("error")
		c, m, r := a.stateErrorResponse(err, "ERR_STATE_SAVE")

		assert.Equal(t, 500, c)
		assert.Equal(t, "error", m)
		assert.Equal(t, "ERR_STATE_SAVE", r.ErrorCode)
	})

	t.Run("etag mismatch error", func(t *testing.T) {
		a := &api{}
		err := state.NewETagError(state.ETagMismatch, errors.New("error"))
		c, m, r := a.stateErrorResponse(err, "ERR_STATE_SAVE")

		assert.Equal(t, 409, c)
		assert.Equal(t, "possible etag mismatch. error from state store: error", m)
		assert.Equal(t, "ERR_STATE_SAVE", r.ErrorCode)
	})

	t.Run("etag invalid error", func(t *testing.T) {
		a := &api{}
		err := state.NewETagError(state.ETagInvalid, errors.New("error"))
		c, m, r := a.stateErrorResponse(err, "ERR_STATE_SAVE")

		assert.Equal(t, 400, c)
		assert.Equal(t, "invalid etag value: error", m)
		assert.Equal(t, "ERR_STATE_SAVE", r.ErrorCode)
	})

	t.Run("etag error mismatch", func(t *testing.T) {
		a := &api{}
		err := state.NewETagError(state.ETagMismatch, errors.New("error"))
		e, c, m := a.etagError(err)

		assert.Equal(t, true, e)
		assert.Equal(t, 409, c)
		assert.Equal(t, "possible etag mismatch. error from state store: error", m)
	})

	t.Run("etag error invalid", func(t *testing.T) {
		a := &api{}
		err := state.NewETagError(state.ETagInvalid, errors.New("error"))
		e, c, m := a.etagError(err)

		assert.Equal(t, true, e)
		assert.Equal(t, 400, c)
		assert.Equal(t, "invalid etag value: error", m)
	})
}

func TestExtractEtag(t *testing.T) {
	t.Run("no etag present", func(t *testing.T) {
		r := fasthttp.RequestCtx{
			Request: fasthttp.Request{},
		}

		ok, etag := extractEtag(&r)
		assert.False(t, ok)
		assert.Empty(t, etag)
	})

	t.Run("empty etag exists", func(t *testing.T) {
		r := fasthttp.RequestCtx{
			Request: fasthttp.Request{},
		}
		r.Request.Header.Add("If-Match", "")

		ok, etag := extractEtag(&r)
		assert.True(t, ok)
		assert.Empty(t, etag)
	})

	t.Run("non-empty etag exists", func(t *testing.T) {
		r := fasthttp.RequestCtx{
			Request: fasthttp.Request{},
		}
		r.Request.Header.Add("If-Match", "a")

		ok, etag := extractEtag(&r)
		assert.True(t, ok)
		assert.Equal(t, "a", etag)
	})
}
