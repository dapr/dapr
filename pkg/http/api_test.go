// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

//nolint:goconst
package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	gohttp "net/http"
	"os"
	"strings"
	"testing"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/exporters"
	"github.com/dapr/components-contrib/exporters/stringexporter"
	"github.com/dapr/components-contrib/middleware"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors"
	http_middleware_loader "github.com/dapr/dapr/pkg/components/middleware/http"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/logger"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	http_middleware "github.com/dapr/dapr/pkg/middleware/http"
	daprt "github.com/dapr/dapr/pkg/testing"
	routing "github.com/fasthttp/router"
	jsoniter "github.com/json-iterator/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

func TestV1OutputBindingsEndpoints(t *testing.T) {
	fakeServer := newFakeHTTPServer()
	testAPI := &api{
		sendToOutputBindingFn: func(name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error) { return nil, nil },
		json:                  jsoniter.ConfigFastest,
	}
	fakeServer.StartServer(testAPI.constructBindingsEndpoints())

	t.Run("Invoke output bindings - 200 OK", func(t *testing.T) {
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
			assert.Equal(t, 200, resp.StatusCode, "failed to invoke output binding with %s", method)
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

	meta := exporters.Metadata{
		Buffer: &buffer,
		Properties: map[string]string{
			"Enabled": "true",
		},
	}
	createExporters(meta)

	testAPI := &api{
		sendToOutputBindingFn: func(name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error) { return nil, nil },
		json:                  jsoniter.ConfigFastest,
		tracingSpec:           spec,
	}
	fakeServer.StartServerWithTracing(spec, testAPI.constructBindingsEndpoints())

	t.Run("Invoke output bindings - 200 OK", func(t *testing.T) {
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
			assert.Equal(t, 200, resp.StatusCode, "failed to invoke output binding with %s", method)
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

	meta := exporters.Metadata{
		Buffer: &buffer,
		Properties: map[string]string{
			"Enabled": "true",
		},
	}
	createExporters(meta)

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
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state/key1"
		testAPI.actor = nil

		testMethods := []string{"GET"}

		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, fakeData, nil)

			// assert
			assert.Equal(t, 400, resp.StatusCode)
			assert.Equal(t, "ERR_ACTOR_RUNTIME_NOT_FOUND", resp.ErrorBody["errorCode"])
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

	t.Run("Transaction - 201 Accepted", func(t *testing.T) {
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
		assert.Equal(t, 201, resp.StatusCode)
		mockActors.AssertNumberOfCalls(t, "TransactionalStateOperation", 1)
	})

	fakeServer.Shutdown()
}

func TestV1MetadataEndpoint(t *testing.T) {
	fakeServer := newFakeHTTPServer()

	testAPI := &api{
		actor: nil,
		json:  jsoniter.ConfigFastest,
	}

	fakeServer.StartServer(testAPI.constructMetadataEndpoints())

	expectedBody := map[string]interface{}{
		"id":       "xyz",
		"actors":   []map[string]interface{}{{"type": "abcd", "count": 10}, {"type": "xyz", "count": 5}},
		"extended": make(map[string]string),
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

func createExporters(meta exporters.Metadata) {
	exporter := stringexporter.NewStringExporter(logger.NewLogger("fakeLogger"))
	exporter.Init("fakeID", "fakeAddress", meta)
}

func TestV1ActorEndpointsWithTracer(t *testing.T) {
	fakeServer := newFakeHTTPServer()

	buffer := ""
	spec := config.TracingSpec{SamplingRate: "1"}

	meta := exporters.Metadata{
		Buffer: &buffer,
		Properties: map[string]string{
			"Enabled": "true",
		},
	}
	createExporters(meta)

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
			assert.Equal(t, 400, resp.StatusCode)
			assert.Equal(t, "ERR_ACTOR_RUNTIME_NOT_FOUND", resp.ErrorBody["errorCode"])
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

	t.Run("Transaction - 201 Accepted", func(t *testing.T) {
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
		assert.Equal(t, 201, resp.StatusCode)
		mockActors.AssertNumberOfCalls(t, "TransactionalStateOperation", 1)
	})

	fakeServer.Shutdown()
}

func TestAPIToken(t *testing.T) {
	token := "1234"

	os.Setenv("DAPR_API_TOKEN", token)
	defer os.Clearenv()

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

	meta := exporters.Metadata{
		Buffer: &buffer,
		Properties: map[string]string{
			"Enabled": "true",
		},
	}
	createExporters(meta)

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
	registry.Register(http_middleware_loader.New("uppercase", func(metadata middleware.Metadata) http_middleware.Middleware {
		return func(h fasthttp.RequestHandler) fasthttp.RequestHandler {
			return func(ctx *fasthttp.RequestCtx) {
				body := string(ctx.PostBody())
				ctx.Request.SetBody([]byte(strings.ToUpper(body)))
				h(ctx)
			}
		}
	}))
	var handlers []http_middleware.Middleware
	for i := 0; i < len(spec.Handlers); i++ {
		handler, err := registry.Create(spec.Handlers[i].Type, middleware.Metadata{})
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

	meta := exporters.Metadata{
		Buffer: &buffer,
		Properties: map[string]string{
			"Enabled": "true",
		},
	}
	createExporters(meta)

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

	meta := exporters.Metadata{
		Buffer: &buffer,
		Properties: map[string]string{
			"Enabled": "true",
		},
	}
	createExporters(meta)

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

// Fake http server and client helpers to simplify endpoints test
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

func (f *fakeHTTPServer) DoRequest(method, path string, body []byte, params map[string]string, headers ...string) fakeHTTPResponse {
	url := fmt.Sprintf("http://localhost/%s", path)
	if params != nil {
		url += "?"
		for k, v := range params {
			url += k + "=" + v + "&"
		}
		url = url[:len(url)-1]
	}
	r, _ := gohttp.NewRequest(method, url, bytes.NewBuffer(body))
	r.Header.Set("Content-Type", "application/json")
	if len(headers) == 1 {
		r.Header.Set("If-Match", headers[0])
	}
	res, err := f.client.Do(r)
	if err != nil {
		panic(fmt.Errorf("failed to request: %v", err))
	}

	bodyBytes, _ := ioutil.ReadAll(res.Body)
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

func TestV1StateEndpoints(t *testing.T) {
	etag := "`~!@#$%^&*()_+-={}[]|\\:\";'<>?,./'"
	fakeServer := newFakeHTTPServer()
	fakeStore := fakeStateStore{}
	fakeStores := map[string]state.Store{
		"store1": fakeStore,
	}
	testAPI := &api{
		stateStores: fakeStores,
		json:        jsoniter.ConfigFastest,
	}
	fakeServer.StartServer(testAPI.constructStateEndpoints())
	storeName := "store1"

	t.Run("Get state - 400 ERR_STATE_STORE_NOT_FOUND", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/bad-key", "notexistStore")
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 400, resp.StatusCode, "reading non-existing store should return 401")
	})

	t.Run("Get state - 204 No Content Found", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/bad-key", storeName)
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 204, resp.StatusCode, "reading non-existing key should return 204")
	})

	t.Run("Get state - Good Key", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/good-key", storeName)
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 200, resp.StatusCode, "reading existing key should succeed")
		assert.Equal(t, etag, resp.RawHeader.Get("ETag"), "failed to read etag")
	})

	t.Run("Update state - No ETag", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s", storeName)
		request := []state.SetRequest{{
			Key:  "good-key",
			ETag: "",
		}}
		b, _ := json.Marshal(request)
		// act
		resp := fakeServer.DoRequest("POST", apiPath, b, nil)
		// assert
		assert.Equal(t, 201, resp.StatusCode, "updating existing key without etag should succeed")
	})

	t.Run("Update state - Matching ETag", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s", storeName)
		request := []state.SetRequest{{
			Key:  "good-key",
			ETag: etag,
		}}
		b, _ := json.Marshal(request)
		// act
		resp := fakeServer.DoRequest("POST", apiPath, b, nil)
		// assert
		assert.Equal(t, 201, resp.StatusCode, "updating existing key with matching etag should succeed")
	})

	t.Run("Update state - Wrong ETag", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s", storeName)
		request := []state.SetRequest{{
			Key:  "good-key",
			ETag: "BAD ETAG",
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
		assert.Equal(t, 200, resp.StatusCode, "updating existing key without etag should succeed")
	})

	t.Run("Delete state - Matching ETag", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/good-key", storeName)
		// act
		resp := fakeServer.DoRequest("DELETE", apiPath, nil, nil, etag)
		// assert
		assert.Equal(t, 200, resp.StatusCode, "updating existing key with matching etag should succeed")
	})

	t.Run("Delete state - Bad ETag", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/good-key", storeName)
		// act
		resp := fakeServer.DoRequest("DELETE", apiPath, nil, nil, "BAD ETAG")
		// assert
		assert.Equal(t, 500, resp.StatusCode, "updating existing key with wrong etag should fail")
	})
}

type fakeStateStore struct {
	counter int
}

func (c fakeStateStore) BulkDelete(req []state.DeleteRequest) error {
	for _, r := range req {
		err := c.Delete(&r)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c fakeStateStore) BulkSet(req []state.SetRequest) error {
	for _, s := range req {
		err := c.Set(&s)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c fakeStateStore) Delete(req *state.DeleteRequest) error {
	if req.Key == "good-key" {
		if req.ETag != "" && req.ETag != "`~!@#$%^&*()_+-={}[]|\\:\";'<>?,./'" {
			return errors.New("ETag mismatch")
		}
		return nil
	}
	return errors.New("NOT FOUND")
}

func (c fakeStateStore) Get(req *state.GetRequest) (*state.GetResponse, error) {
	if req.Key == "good-key" {
		return &state.GetResponse{
			Data: []byte("life is good"),
			ETag: "`~!@#$%^&*()_+-={}[]|\\:\";'<>?,./'",
		}, nil
	}
	return nil, nil
}

func (c fakeStateStore) Init(metadata state.Metadata) error {
	c.counter = 0
	return nil
}

func (c fakeStateStore) Set(req *state.SetRequest) error {
	if req.Key == "good-key" {
		if req.ETag != "" && req.ETag != "`~!@#$%^&*()_+-={}[]|\\:\";'<>?,./'" {
			return errors.New("ETag mismatch")
		}
		return nil
	}
	return errors.New("NOT FOUND")
}

func (c fakeStateStore) Multi(request *state.TransactionalStateRequest) error {
	return nil
}

func TestV1SecretEndpoints(t *testing.T) {
	fakeServer := newFakeHTTPServer()
	fakeStore := fakeSecretStore{}
	fakeStores := map[string]secretstores.SecretStore{
		"store1": fakeStore,
	}
	testAPI := &api{
		secretStores: fakeStores,
		json:         jsoniter.ConfigFastest,
	}
	fakeServer.StartServer(testAPI.constructSecretEndpoints())
	storeName := "store1"

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
	})

	t.Run("Get secret - Good Key", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/secrets/%s/good-key", storeName)
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 200, resp.StatusCode, "reading existing key should succeed")
	})
}

type fakeSecretStore struct {
}

func (c fakeSecretStore) GetSecret(req secretstores.GetSecretRequest) (secretstores.GetSecretResponse, error) {
	if req.Name == "good-key" {
		return secretstores.GetSecretResponse{
			Data: map[string]string{"good-key": "life is good"},
		}, nil
	}
	return secretstores.GetSecretResponse{Data: nil}, nil
}

func (c fakeSecretStore) Init(metadata secretstores.Metadata) error {
	return nil
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

	t.Run("Healthz - 200 OK", func(t *testing.T) {
		apiPath := "v1.0/healthz"
		testAPI.MarkStatusAsReady()
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)

		assert.Equal(t, 200, resp.StatusCode)
	})

	fakeServer.Shutdown()
}

func TestV1TransactionEndpoints(t *testing.T) {
	fakeServer := newFakeHTTPServer()
	fakeStore := fakeStateStore{}
	fakeStores := map[string]state.Store{
		"store1": fakeStore,
	}
	testAPI := &api{
		stateStores: fakeStores,
		json:        jsoniter.ConfigFastest,
	}
	fakeServer.StartServer(testAPI.constructStateEndpoints())
	fakeBodyObject := map[string]interface{}{"data": "fakeData"}
	storeName := "store1"

	t.Run("Direct Transaction - 201 Accepted", func(t *testing.T) {
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
		assert.Equal(t, 201, resp.StatusCode, "Dapr should return 201")
	})

	t.Run("Post non-existent state store - 401 No State Store Found", func(t *testing.T) {
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
		assert.Equal(t, 401, resp.StatusCode, "Accessing non-existent state store should return 401")
	})

	fakeServer.Shutdown()
}
