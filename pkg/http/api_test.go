// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

//nolint:goconst
package http

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	gohttp "net/http"
	"strings"
	"testing"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/exporters"
	"github.com/dapr/components-contrib/exporters/stringexporter"
	"github.com/dapr/components-contrib/middleware"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/channel/http"
	http_middleware_loader "github.com/dapr/dapr/pkg/components/middleware/http"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/logger"
	"github.com/dapr/dapr/pkg/messaging"
	http_middleware "github.com/dapr/dapr/pkg/middleware/http"
	daprt "github.com/dapr/dapr/pkg/testing"
	jsoniter "github.com/json-iterator/go"
	routing "github.com/qiangxue/fasthttp-routing"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

var retryCounter = 0

func TestSetHeaders(t *testing.T) {
	testAPI := &api{}
	c := &routing.Context{}
	c.RequestCtx = &fasthttp.RequestCtx{Request: fasthttp.Request{}}
	c.Request.Header.Set("H1", "v1")
	c.Request.Header.Set("H2", "v2")
	m := map[string]string{}
	testAPI.setHeaders(c, m)
	assert.Equal(t, "H1&__header_equals__&v1&__header_delim__&H2&__header_equals__&v2", m["headers"])
}

func TestV1OutputBindingsEndpoints(t *testing.T) {
	fakeServer := newFakeHTTPServer()
	testAPI := &api{
		sendToOutputBindingFn: func(name string, req *bindings.WriteRequest) error { return nil },
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

		testAPI.sendToOutputBindingFn = func(name string, req *bindings.WriteRequest) error {
			return errors.New("missing binding name")
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
	spec := config.TracingSpec{Enabled: true}

	meta := exporters.Metadata{
		Buffer: &buffer,
		Properties: map[string]string{
			"Enabled": "true",
		},
	}
	createExporters(meta)

	testAPI := &api{
		sendToOutputBindingFn: func(name string, req *bindings.WriteRequest) error { return nil },
		json:                  jsoniter.ConfigFastest,
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
			assert.Equal(t, "0", buffer, "failed to generate proper traces with %s", method)
		}
	})

	t.Run("Invoke output bindings - 500 InternalError", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/bindings/notfound", apiVersionV1)
		req := OutputBindingRequest{
			Data: "fake output",
		}
		b, _ := json.Marshal(&req)

		testAPI.sendToOutputBindingFn = func(name string, req *bindings.WriteRequest) error {
			return errors.New("missing binding name")
		}

		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			buffer = ""
			// act
			resp := fakeServer.DoRequest(method, apiPath, b, nil)

			// assert
			assert.Equal(t, 500, resp.StatusCode)
			assert.Equal(t, "ERR_INVOKE_OUTPUT_BINDING", resp.ErrorBody["errorCode"])
			assert.Equal(t, "13", buffer, "failed to generate proper traces with %s", method)
		}
	})

	fakeServer.Shutdown()
}

func TestV1DirectMessagingEndpoints(t *testing.T) {
	fakeHeader := "Host&__header_equals__&localhost&__header_delim__&Content-Length&__header_equals__&8&__header_delim__&Content-Type&__header_equals__&application/json&__header_delim__&User-Agent&__header_equals__&Go-http-client/1.1&__header_delim__&Accept-Encoding&__header_equals__&gzip"
	fakeDirectMessageResponse := &messaging.DirectMessageResponse{
		Data: []byte("fakeDirectMessageResponse"),
		Metadata: map[string]string{
			"http.status_code": "200",
			"headers":          fakeHeader,
		},
	}

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

		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On(
			"Invoke",
			&messaging.DirectMessageRequest{
				Data:   fakeData,
				Method: "fakeMethod",
				Metadata: map[string]string{
					"headers":        fakeHeader,
					http.HTTPVerb:    "POST",
					http.QueryString: "", // without query string
				},
				Target: "fakeAppID",
			}).Return(fakeDirectMessageResponse, nil).Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 200, resp.StatusCode)
	})

	t.Run("Invoke direct messaging with querystring - 200 OK", func(t *testing.T) {
		apiPath := "v1.0/invoke/fakeAppID/method/fakeMethod?param1=val1&param2=val2"
		fakeData := []byte("fakeData")

		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On(
			"Invoke",
			&messaging.DirectMessageRequest{
				Data:   fakeData,
				Method: "fakeMethod",
				Metadata: map[string]string{
					"headers":        fakeHeader,
					http.HTTPVerb:    "POST",
					http.QueryString: "param1=val1&param2=val2",
				},
				Target: "fakeAppID",
			}).Return(fakeDirectMessageResponse, nil).Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 200, resp.StatusCode)
	})

	fakeServer.Shutdown()
}

func TestV1DirectMessagingEndpointsWithTracer(t *testing.T) {
	fakeHeader := "Host&__header_equals__&localhost&__header_delim__&Content-Length&__header_equals__&8&__header_delim__&Content-Type&__header_equals__&application/json&__header_delim__&User-Agent&__header_equals__&Go-http-client/1.1&__header_delim__&Accept-Encoding&__header_equals__&gzip"
	fakeDirectMessageResponse := &messaging.DirectMessageResponse{
		Data: []byte("fakeDirectMessageResponse"),
		Metadata: map[string]string{
			"http.status_code": "200",
			"headers":          fakeHeader,
		},
	}

	mockDirectMessaging := new(daprt.MockDirectMessaging)

	fakeServer := newFakeHTTPServer()

	buffer := ""
	spec := config.TracingSpec{Enabled: true}

	meta := exporters.Metadata{
		Buffer: &buffer,
		Properties: map[string]string{
			"Enabled": "true",
		},
	}
	createExporters(meta)

	testAPI := &api{
		directMessaging: mockDirectMessaging,
	}
	fakeServer.StartServerWithTracing(spec, testAPI.constructDirectMessagingEndpoints())

	t.Run("Invoke direct messaging without querystring - 200 OK", func(t *testing.T) {
		buffer = ""
		apiPath := "v1.0/invoke/fakeAppID/method/fakeMethod"
		fakeData := []byte("fakeData")

		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On(
			"Invoke",
			&messaging.DirectMessageRequest{
				Data:   fakeData,
				Method: "fakeMethod",
				Metadata: map[string]string{
					"headers":        fakeHeader,
					http.HTTPVerb:    "POST",
					http.QueryString: "", // without query string
				},
				Target: "fakeAppID",
			}).Return(fakeDirectMessageResponse, nil).Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, "0", buffer, "failed to generate proper traces with invoke")
		assert.Equal(t, 200, resp.StatusCode)
	})

	t.Run("Invoke direct messaging with querystring - 200 OK", func(t *testing.T) {
		buffer = ""
		apiPath := "v1.0/invoke/fakeAppID/method/fakeMethod?param1=val1&param2=val2"
		fakeData := []byte("fakeData")

		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On(
			"Invoke",
			&messaging.DirectMessageRequest{
				Data:   fakeData,
				Method: "fakeMethod",
				Metadata: map[string]string{
					"headers":        fakeHeader,
					http.HTTPVerb:    "POST",
					http.QueryString: "param1=val1&param2=val2",
				},
				Target: "fakeAppID",
			}).Return(fakeDirectMessageResponse, nil).Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, "0", buffer, "failed to generate proper traces with invoke")
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

		testMethods := []string{"POST", "PUT", "GET", "DELETE"}

		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, fakeData, nil)

			// assert
			assert.Equal(t, 400, resp.StatusCode)
			assert.Equal(t, "ERR_ACTOR_RUNTIME_NOT_FOUND", resp.ErrorBody["errorCode"])
		}
	})

	t.Run("Save actor state - 200 OK", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state/key1"
		mockActors := new(daprt.MockActors)
		mockActors.On("SaveState", &actors.SaveStateRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
			Key:       "key1",
			Value:     fakeBodyObject,
		}).Return(nil)

		mockActors.On("IsActorHosted", &actors.ActorHostedRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
		}).Return(true)

		testAPI.actor = mockActors

		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			mockActors.Calls = nil

			// act
			resp := fakeServer.DoRequest(method, apiPath, fakeData, nil)

			// assert
			assert.Equal(t, 201, resp.StatusCode, "failed to save state key with %s", method)
			mockActors.AssertNumberOfCalls(t, "SaveState", 1)
		}
	})

	t.Run("Save byte array state value - 200 OK", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state/bytearray"

		fakeBodyArray := []byte{0x01, 0x02, 0x03, 0x06, 0x10}

		serializedByteArray, _ := json.Marshal(fakeBodyArray)
		encodedLen := base64.StdEncoding.EncodedLen(len(fakeBodyArray))
		base64Encoded := make([]byte, encodedLen)
		base64.StdEncoding.Encode(base64Encoded, fakeBodyArray)

		assert.Equal(t, base64Encoded, serializedByteArray[1:len(serializedByteArray)-1], "serialized byte array must be base64-encoded data")

		mockActors := new(daprt.MockActors)
		mockActors.On("SaveState", &actors.SaveStateRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
			Key:       "bytearray",
			Value:     string(base64Encoded),
		}).Return(nil)

		mockActors.On("IsActorHosted", &actors.ActorHostedRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
		}).Return(true)

		testAPI.actor = mockActors

		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			mockActors.Calls = nil

			// act
			resp := fakeServer.DoRequest(method, apiPath, serializedByteArray, nil)

			// assert
			assert.Equal(t, 201, resp.StatusCode, "failed to save state key with %s", method)
			mockActors.AssertNumberOfCalls(t, "SaveState", 1)
		}
	})

	t.Run("Save object which has byte-array member - 200 OK", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state/bytearray"

		fakeBodyArray := []byte{0x01, 0x02, 0x03, 0x06, 0x10}

		fakeResp := map[string]interface{}{
			"data":  "fakeData",
			"data2": fakeBodyArray,
		}

		serializedByteArray, _ := json.Marshal(fakeResp)

		encodedLen := base64.StdEncoding.EncodedLen(len(fakeBodyArray))
		base64Encoded := make([]byte, encodedLen)
		base64.StdEncoding.Encode(base64Encoded, fakeBodyArray)

		expectedObj := map[string]interface{}{
			"data":  "fakeData",
			"data2": string(base64Encoded),
		}

		mockActors := new(daprt.MockActors)
		mockActors.On("SaveState", &actors.SaveStateRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
			Key:       "bytearray",
			Value:     expectedObj,
		}).Return(nil)

		mockActors.On("IsActorHosted", &actors.ActorHostedRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
		}).Return(true)

		testAPI.actor = mockActors

		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			mockActors.Calls = nil

			// act
			resp := fakeServer.DoRequest(method, apiPath, serializedByteArray, nil)

			// assert
			assert.Equal(t, 201, resp.StatusCode, "failed to save state key with %s", method)
			mockActors.AssertNumberOfCalls(t, "SaveState", 1)
		}
	})

	t.Run("Save actor state - 400 deserialization error", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state/key1"
		nonJSONFakeData := []byte("{\"key\":}")

		mockActors := new(daprt.MockActors)
		mockActors.On("SaveState", &actors.SaveStateRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
			Key:       "key1",
			Value:     nonJSONFakeData,
		}).Return(nil)

		mockActors.On("IsActorHosted", &actors.ActorHostedRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
		}).Return(true)

		testAPI.actor = mockActors

		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			mockActors.Calls = nil

			// act
			resp := fakeServer.DoRequest(method, apiPath, nonJSONFakeData, nil)

			// assert
			assert.Equal(t, 400, resp.StatusCode)
			mockActors.AssertNumberOfCalls(t, "SaveState", 0)
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

		testAPI.actor = mockActors

		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)

		// assert
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, fakeData, resp.RawBody)
		mockActors.AssertNumberOfCalls(t, "GetState", 1)
	})

	t.Run("Delete actor state - 200 OK", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state/key1"
		mockActors := new(daprt.MockActors)
		mockActors.On("DeleteState", &actors.DeleteStateRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
			Key:       "key1",
		}).Return(nil)

		mockActors.On("IsActorHosted", &actors.ActorHostedRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
		}).Return(true)

		testAPI.actor = mockActors

		// act
		resp := fakeServer.DoRequest("DELETE", apiPath, nil, nil)

		// assert
		assert.Equal(t, 200, resp.StatusCode)
		mockActors.AssertNumberOfCalls(t, "DeleteState", 1)
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
		"id":     "xyz",
		"actors": []map[string]interface{}{{"type": "abcd", "count": 10}, {"type": "xyz", "count": 5}},
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
	spec := config.TracingSpec{Enabled: true}

	meta := exporters.Metadata{
		Buffer: &buffer,
		Properties: map[string]string{
			"Enabled": "true",
		},
	}
	createExporters(meta)

	testAPI := &api{
		actor: nil,
		json:  jsoniter.ConfigFastest,
	}

	fakeServer.StartServerWithTracing(spec, testAPI.constructActorEndpoints())

	fakeBodyObject := map[string]interface{}{"data": "fakeData"}
	fakeData, _ := json.Marshal(fakeBodyObject)

	t.Run("Actor runtime is not initialized", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state/key1"
		testAPI.actor = nil

		testMethods := []string{"POST", "PUT", "GET", "DELETE"}

		for _, method := range testMethods {
			buffer = ""
			// act
			resp := fakeServer.DoRequest(method, apiPath, fakeData, nil)

			// assert
			assert.Equal(t, 400, resp.StatusCode)
			assert.Equal(t, "ERR_ACTOR_RUNTIME_NOT_FOUND", resp.ErrorBody["errorCode"])
			assert.Equal(t, "3", buffer, "failed to generate proper traces with %s", method)
		}
	})

	t.Run("Save actor state - 200 OK", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state/key1"
		mockActors := new(daprt.MockActors)
		mockActors.On("SaveState", &actors.SaveStateRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
			Key:       "key1",
			Value:     fakeBodyObject,
		}).Return(nil)

		mockActors.On("IsActorHosted", &actors.ActorHostedRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
		}).Return(true)

		testAPI.actor = mockActors

		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			buffer = ""
			mockActors.Calls = nil

			// act
			resp := fakeServer.DoRequest(method, apiPath, fakeData, nil)

			// assert
			assert.Equal(t, 201, resp.StatusCode, "failed to save state key with %s", method)
			assert.Equal(t, "0", buffer, "failed to generate proper traces with %s", method)
			mockActors.AssertNumberOfCalls(t, "SaveState", 1)
		}
	})

	t.Run("Save byte array state value - 200 OK", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state/bytearray"

		fakeBodyArray := []byte{0x01, 0x02, 0x03, 0x06, 0x10}

		serializedByteArray, _ := json.Marshal(fakeBodyArray)
		encodedLen := base64.StdEncoding.EncodedLen(len(fakeBodyArray))
		base64Encoded := make([]byte, encodedLen)
		base64.StdEncoding.Encode(base64Encoded, fakeBodyArray)

		assert.Equal(t, base64Encoded, serializedByteArray[1:len(serializedByteArray)-1], "serialized byte array must be base64-encoded data")

		mockActors := new(daprt.MockActors)
		mockActors.On("SaveState", &actors.SaveStateRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
			Key:       "bytearray",
			Value:     string(base64Encoded),
		}).Return(nil)

		mockActors.On("IsActorHosted", &actors.ActorHostedRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
		}).Return(true)

		testAPI.actor = mockActors

		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			buffer = ""
			mockActors.Calls = nil

			// act
			resp := fakeServer.DoRequest(method, apiPath, serializedByteArray, nil)

			// assert
			assert.Equal(t, 201, resp.StatusCode, "failed to save state key with %s", method)
			assert.Equal(t, "0", buffer, "failed to generate proper traces with %s", method)
			mockActors.AssertNumberOfCalls(t, "SaveState", 1)
		}
	})

	t.Run("Save object which has byte-array member - 200 OK", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state/bytearray"

		fakeBodyArray := []byte{0x01, 0x02, 0x03, 0x06, 0x10}

		fakeObj := map[string]interface{}{
			"data":  "fakeData",
			"data2": fakeBodyArray,
		}

		serializedByteArray, _ := json.Marshal(fakeObj)

		encodedLen := base64.StdEncoding.EncodedLen(len(fakeBodyArray))
		base64Encoded := make([]byte, encodedLen)
		base64.StdEncoding.Encode(base64Encoded, fakeBodyArray)

		expectedObj := map[string]interface{}{
			"data":  "fakeData",
			"data2": string(base64Encoded),
		}

		mockActors := new(daprt.MockActors)
		mockActors.On("SaveState", &actors.SaveStateRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
			Key:       "bytearray",
			Value:     expectedObj,
		}).Return(nil)

		mockActors.On("IsActorHosted", &actors.ActorHostedRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
		}).Return(true)

		testAPI.actor = mockActors

		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			buffer = ""
			mockActors.Calls = nil

			// act
			resp := fakeServer.DoRequest(method, apiPath, serializedByteArray, nil)

			// assert
			assert.Equal(t, 201, resp.StatusCode, "failed to save state key with %s", method)
			assert.Equal(t, "0", buffer, "failed to generate proper traces with %s", method)
			mockActors.AssertNumberOfCalls(t, "SaveState", 1)
		}
	})

	t.Run("Save actor state - 400 deserialization error", func(t *testing.T) {
		buffer = ""
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state/key1"
		nonJSONFakeData := []byte("{\"key\":}")

		mockActors := new(daprt.MockActors)
		mockActors.On("SaveState", &actors.SaveStateRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
			Key:       "key1",
			Value:     nonJSONFakeData,
		}).Return(nil)

		mockActors.On("IsActorHosted", &actors.ActorHostedRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
		}).Return(true)

		testAPI.actor = mockActors

		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			mockActors.Calls = nil

			// act
			resp := fakeServer.DoRequest(method, apiPath, nonJSONFakeData, nil)

			// assert
			assert.Equal(t, 400, resp.StatusCode)
			assert.Equal(t, "3", buffer, "failed to generate proper traces for saving actor state")
			mockActors.AssertNumberOfCalls(t, "SaveState", 0)
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

		testAPI.actor = mockActors

		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)

		// assert
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, "0", buffer, "failed to generate proper traces for getting actor state")
		assert.Equal(t, fakeData, resp.RawBody)
		mockActors.AssertNumberOfCalls(t, "GetState", 1)
	})

	t.Run("Delete actor state - 200 OK", func(t *testing.T) {
		buffer = ""
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state/key1"
		mockActors := new(daprt.MockActors)
		mockActors.On("DeleteState", &actors.DeleteStateRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
			Key:       "key1",
		}).Return(nil)

		mockActors.On("IsActorHosted", &actors.ActorHostedRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
		}).Return(true)

		testAPI.actor = mockActors

		// act
		resp := fakeServer.DoRequest("DELETE", apiPath, nil, nil)

		// assert
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, "0", buffer, "failed to generate proper traces for deleting actor state")
		mockActors.AssertNumberOfCalls(t, "DeleteState", 1)
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
		assert.Equal(t, "0", buffer, "failed to generate proper traces for transaction")
		mockActors.AssertNumberOfCalls(t, "TransactionalStateOperation", 1)
	})

	fakeServer.Shutdown()
}

func TestEmptyPipelineWithTracer(t *testing.T) {
	fakeHeader := "Host&__header_equals__&localhost&__header_delim__&Content-Length&__header_equals__&8&__header_delim__&Content-Type&__header_equals__&application/json&__header_delim__&User-Agent&__header_equals__&Go-http-client/1.1&__header_delim__&Accept-Encoding&__header_equals__&gzip"
	fakeDirectMessageResponse := &messaging.DirectMessageResponse{
		Data: []byte("fakeDirectMessageResponse"),
		Metadata: map[string]string{
			"http.status_code": "200",
			"headers":          fakeHeader,
		},
	}

	mockDirectMessaging := new(daprt.MockDirectMessaging)

	fakeServer := newFakeHTTPServer()

	buffer := ""
	spec := config.TracingSpec{Enabled: true}
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
	}
	fakeServer.StartServerWithTracingAndPipeline(spec, pipe, testAPI.constructDirectMessagingEndpoints())

	t.Run("Invoke direct messaging without querystring - 200 OK", func(t *testing.T) {
		buffer = ""
		apiPath := "v1.0/invoke/fakeDaprID/method/fakeMethod"
		fakeData := []byte("fakeData")

		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On(
			"Invoke",
			&messaging.DirectMessageRequest{
				Data:   fakeData,
				Method: "fakeMethod",
				Metadata: map[string]string{
					"headers":        fakeHeader,
					http.HTTPVerb:    "POST",
					http.QueryString: "", // without query string
				},
				Target: "fakeDaprID",
			}).Return(fakeDirectMessageResponse, nil).Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, "0", buffer, "failed to generate proper traces with invoke")
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
	fakeHeader := "Host&__header_equals__&localhost&__header_delim__&Content-Length&__header_equals__&8&__header_delim__&Content-Type&__header_equals__&application/json&__header_delim__&User-Agent&__header_equals__&Go-http-client/1.1&__header_delim__&Accept-Encoding&__header_equals__&gzip"
	fakeDirectMessageResponse := &messaging.DirectMessageResponse{
		Data: []byte("fakeDirectMessageResponse"),
		Metadata: map[string]string{
			"http.status_code": "200",
			"headers":          fakeHeader,
		},
	}

	mockDirectMessaging := new(daprt.MockDirectMessaging)

	fakeServer := newFakeHTTPServer()

	buffer := ""
	spec := config.TracingSpec{Enabled: true}

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
	}
	fakeServer.StartServerWithTracingAndPipeline(spec, pipeline, testAPI.constructDirectMessagingEndpoints())

	t.Run("Invoke direct messaging without querystring - 200 OK", func(t *testing.T) {
		buffer = ""
		apiPath := "v1.0/invoke/fakeDaprID/method/fakeMethod"
		fakeData := []byte("fakeData")

		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On(
			"Invoke",
			&messaging.DirectMessageRequest{
				Data:   []byte("FAKEDATA"),
				Method: "fakeMethod",
				Metadata: map[string]string{
					"headers":        fakeHeader,
					http.HTTPVerb:    "POST",
					http.QueryString: "", // without query string
				},
				Target: "fakeDaprID",
			}).Return(fakeDirectMessageResponse, nil).Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, "0", buffer, "failed to generate proper traces with invoke")
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
		if err := fasthttp.Serve(f.ln, router.HandleRequest); err != nil {
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
		if err := fasthttp.Serve(f.ln, diag.TracingHTTPMiddleware(spec, router.HandleRequest)); err != nil {
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
		handler := pipeline.Apply(router.HandleRequest)
		if err := fasthttp.Serve(f.ln, diag.TracingHTTPMiddleware(spec, handler)); err != nil {
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

func (f *fakeHTTPServer) getRouter(endpoints []Endpoint) *routing.Router {
	router := routing.New()

	for _, e := range endpoints {
		methods := strings.Join(e.Methods, ",")
		path := fmt.Sprintf("/%s/%s", e.Version, e.Route)

		router.To(methods, path, e.Handler)
	}

	return router
}

func (f *fakeHTTPServer) Shutdown() {
	f.ln.Close()
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
	t.Run("Get state - 401 ERR_STATE_STORE_NOT_FOUND", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/bad-key", "notexistStore")
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 401, resp.StatusCode, "reading non-existing store should return 401")
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
	t.Run("Delete state - With Retries", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/failed-key", storeName)
		retryCounter = 0
		// act
		_ = fakeServer.DoRequest("DELETE", apiPath, nil, map[string]string{
			"retryInterval":  "100",
			"retryPattern":   "linear",
			"retryThreshold": "3",
		}, "BAD ETAG")
		// assert
		assert.Equal(t, 3, retryCounter, "should have tried 3 times")
	})
	t.Run("Set state - With Retries", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s", storeName)
		retryCounter = 0
		request := []state.SetRequest{{
			Key:  "failed-key",
			ETag: "BAD ETAG",
			Options: state.SetStateOption{
				RetryPolicy: state.RetryPolicy{
					Interval:  100,
					Pattern:   state.Linear,
					Threshold: 5,
				},
			},
		}}
		b, _ := json.Marshal(request)

		// act
		_ = fakeServer.DoRequest("POST", apiPath, b, nil, "BAD ETAG")
		// assert
		assert.Equal(t, 5, retryCounter, "should have tried 5 times")
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
	} else if req.Key == "failed-key" {
		return state.DeleteWithRetries(func(req *state.DeleteRequest) error {
			retryCounter++
			if retryCounter < 3 {
				return errors.New("Simulated failure")
			}
			return nil
		}, req)
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
	} else if req.Key == "failed-key" {
		return state.SetWithRetries(func(req *state.SetRequest) error {
			retryCounter++
			if retryCounter < 5 {
				return errors.New("Simulated failure")
			}
			return nil
		}, req)
	}
	return errors.New("NOT FOUND")
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
