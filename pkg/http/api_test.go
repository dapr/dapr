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

	"github.com/actionscore/actions/pkg/actors"
	"github.com/actionscore/actions/pkg/channel/http"
	"github.com/actionscore/actions/pkg/components/state"
	"github.com/actionscore/actions/pkg/config"
	diag "github.com/actionscore/actions/pkg/diagnostics"
	"github.com/actionscore/actions/pkg/messaging"
	actionst "github.com/actionscore/actions/pkg/testing"
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
	request := fasthttp.Request{}
	c.RequestCtx = &fasthttp.RequestCtx{Request: request}
	c.Request.Header.Set("H1", "v1")
	c.Request.Header.Set("H2", "v2")
	m := map[string]string{}
	testAPI.setHeaders(c, m)
	assert.Equal(t, "H1&__header_equals__&v1&__header_delim__&H2&__header_equals__&v2", m["headers"])
}

func TestV1OutputBindingsEndpoints(t *testing.T) {
	fakeServer := newFakeHTTPServer()
	testAPI := &api{
		sendToOutputBindingFn: func(name string, data []byte) error { return nil },
		json:                  jsoniter.ConfigFastest,
	}
	fakeServer.StartServer(testAPI.constructBindingsEndpoints())

	t.Run("Invoke output bindings - 200 OK", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/bindings/testbinding", apiVersionV1)
		fakeData := []byte("fake output")

		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, fakeData, nil)

			// assert
			assert.Equal(t, 200, resp.StatusCode, "failed to invoke output binding with %s", method)
		}
	})

	t.Run("Invoke output bindings - 500 InternalError", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/bindings/notfound", apiVersionV1)
		fakeData := []byte("fake output")

		testAPI.sendToOutputBindingFn = func(name string, data []byte) error {
			return errors.New("missing binding name")
		}

		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, fakeData, nil)

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
	spec := config.TracingSpec{ExporterType: "string"}
	diag.CreateExporter("", "", spec, &buffer)
	testAPI := &api{
		sendToOutputBindingFn: func(name string, data []byte) error { return nil },
	}
	fakeServer.StartServerWithTracing(spec, testAPI.constructBindingsEndpoints())

	t.Run("Invoke output bindings - 200 OK", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/bindings/testbinding", apiVersionV1)
		fakeData := []byte("fake output")

		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			buffer = ""
			// act
			resp := fakeServer.DoRequest(method, apiPath, fakeData, nil)

			// assert
			assert.Equal(t, 200, resp.StatusCode, "failed to invoke output binding with %s", method)
			assert.Equal(t, "0", buffer, "failed to generate proper traces with %s", method)
		}
	})

	t.Run("Invoke output bindings - 500 InternalError", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/bindings/notfound", apiVersionV1)
		fakeData := []byte("fake output")

		testAPI.sendToOutputBindingFn = func(name string, data []byte) error {
			return errors.New("missing binding name")
		}

		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			buffer = ""
			// act
			resp := fakeServer.DoRequest(method, apiPath, fakeData, nil)

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

	mockDirectMessaging := new(actionst.MockDirectMessaging)

	fakeServer := newFakeHTTPServer()
	testAPI := &api{
		directMessaging: mockDirectMessaging,
		json:            jsoniter.ConfigFastest,
	}
	fakeServer.StartServer(testAPI.constructDirectMessagingEndpoints())

	t.Run("Invoke direct messaging without querystring - 200 OK", func(t *testing.T) {
		apiPath := "v1.0/actions/fakeActionsID/fakeMethod"
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
				Target: "fakeActionsID",
			}).Return(fakeDirectMessageResponse, nil).Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 200, resp.StatusCode)
	})

	t.Run("Invoke direct messaging with querystring - 200 OK", func(t *testing.T) {
		apiPath := "v1.0/actions/fakeActionsID/fakeMethod?param1=val1&param2=val2"
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
				Target: "fakeActionsID",
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

	mockDirectMessaging := new(actionst.MockDirectMessaging)

	fakeServer := newFakeHTTPServer()

	buffer := ""
	spec := config.TracingSpec{ExporterType: "string"}
	diag.CreateExporter("", "", spec, &buffer)

	testAPI := &api{
		directMessaging: mockDirectMessaging,
	}
	fakeServer.StartServerWithTracing(spec, testAPI.constructDirectMessagingEndpoints())

	t.Run("Invoke direct messaging without querystring - 200 OK", func(t *testing.T) {
		buffer = ""
		apiPath := "v1.0/actions/fakeActionsID/fakeMethod"
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
				Target: "fakeActionsID",
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
		apiPath := "v1.0/actions/fakeActionsID/fakeMethod?param1=val1&param2=val2"
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
				Target: "fakeActionsID",
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
		mockActors := new(actionst.MockActors)
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

		mockActors := new(actionst.MockActors)
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

		fakeBodyObject := map[string]interface{}{
			"data":  "fakeData",
			"data2": fakeBodyArray,
		}

		serializedByteArray, _ := json.Marshal(fakeBodyObject)

		encodedLen := base64.StdEncoding.EncodedLen(len(fakeBodyArray))
		base64Encoded := make([]byte, encodedLen)
		base64.StdEncoding.Encode(base64Encoded, fakeBodyArray)

		expectedObj := map[string]interface{}{
			"data":  "fakeData",
			"data2": string(base64Encoded),
		}

		mockActors := new(actionst.MockActors)
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

		mockActors := new(actionst.MockActors)
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
		mockActors := new(actionst.MockActors)
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
		mockActors := new(actionst.MockActors)
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
			actors.TransactionalOperation{
				Operation: actors.Upsert,
				Request: map[string]interface{}{
					"key":   "fakeKey1",
					"value": fakeBodyObject,
				},
			},
			actors.TransactionalOperation{
				Operation: actors.Delete,
				Request: map[string]interface{}{
					"key": "fakeKey1",
				},
			},
		}

		mockActors := new(actionst.MockActors)
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

func TestV1ActorEndpointsWithTracer(t *testing.T) {
	fakeServer := newFakeHTTPServer()

	buffer := ""
	spec := config.TracingSpec{ExporterType: "string"}
	diag.CreateExporter("", "", spec, &buffer)

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
		mockActors := new(actionst.MockActors)
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

		mockActors := new(actionst.MockActors)
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

		fakeBodyObject := map[string]interface{}{
			"data":  "fakeData",
			"data2": fakeBodyArray,
		}

		serializedByteArray, _ := json.Marshal(fakeBodyObject)

		encodedLen := base64.StdEncoding.EncodedLen(len(fakeBodyArray))
		base64Encoded := make([]byte, encodedLen)
		base64.StdEncoding.Encode(base64Encoded, fakeBodyArray)

		expectedObj := map[string]interface{}{
			"data":  "fakeData",
			"data2": string(base64Encoded),
		}

		mockActors := new(actionst.MockActors)
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

		mockActors := new(actionst.MockActors)
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
		mockActors := new(actionst.MockActors)
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
		mockActors := new(actionst.MockActors)
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
			actors.TransactionalOperation{
				Operation: actors.Upsert,
				Request: map[string]interface{}{
					"key":   "fakeKey1",
					"value": fakeBodyObject,
				},
			},
			actors.TransactionalOperation{
				Operation: actors.Delete,
				Request: map[string]interface{}{
					"key": "fakeKey1",
				},
			},
		}

		mockActors := new(actionst.MockActors)
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
	testAPI := &api{
		stateStore: fakeStore,
		json:       jsoniter.ConfigFastest,
	}
	fakeServer.StartServer(testAPI.constructStateEndpoints())
	t.Run("Get state - 404 Not Found", func(t *testing.T) {
		apiPath := "v1.0/state/bad-key"
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 404, resp.StatusCode, "reading non-existing key should return 404")
	})
	t.Run("Get state - Good Key", func(t *testing.T) {
		apiPath := "v1.0/state/good-key"
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 200, resp.StatusCode, "reading existing key should succeed")
		assert.Equal(t, etag, resp.RawHeader.Get("ETag"), "failed to read etag")
	})
	t.Run("Update state - No ETag", func(t *testing.T) {
		apiPath := "v1.0/state"
		request := []state.SetRequest{state.SetRequest{
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
		apiPath := "v1.0/state"
		request := []state.SetRequest{state.SetRequest{
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
		apiPath := "v1.0/state"
		request := []state.SetRequest{state.SetRequest{
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
		apiPath := "v1.0/state/good-key"
		// act
		resp := fakeServer.DoRequest("DELETE", apiPath, nil, nil)
		// assert
		assert.Equal(t, 200, resp.StatusCode, "updating existing key without etag should succeed")
	})
	t.Run("Delete state - Matching ETag", func(t *testing.T) {
		apiPath := "v1.0/state/good-key"
		// act
		resp := fakeServer.DoRequest("DELETE", apiPath, nil, nil, etag)
		// assert
		assert.Equal(t, 200, resp.StatusCode, "updating existing key with matching etag should succeed")
	})
	t.Run("Delete state - Bad ETag", func(t *testing.T) {
		apiPath := "v1.0/state/good-key"
		// act
		resp := fakeServer.DoRequest("DELETE", apiPath, nil, nil, "BAD ETAG")
		// assert
		assert.Equal(t, 500, resp.StatusCode, "updating existing key with wrong etag should fail")
	})
	t.Run("Delete state - With Retries", func(t *testing.T) {
		apiPath := "v1.0/state/failed-key"
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
	}
	return errors.New("NOT FOUND")
}
