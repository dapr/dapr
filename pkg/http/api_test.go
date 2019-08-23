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
	"github.com/actionscore/actions/pkg/messaging"
	actionst "github.com/actionscore/actions/pkg/testing"
	jsoniter "github.com/json-iterator/go"
	routing "github.com/qiangxue/fasthttp-routing"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

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
			resp := fakeServer.DoRequest(method, apiPath, fakeData)

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
			resp := fakeServer.DoRequest(method, apiPath, fakeData)

			// assert
			assert.Equal(t, 500, resp.StatusCode)
			assert.Equal(t, "ERR_INVOKE_OUTPUT_BINDING", resp.ErrorBody["errorCode"])
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
		resp := fakeServer.DoRequest("POST", apiPath, fakeData)

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
		resp := fakeServer.DoRequest("POST", apiPath, fakeData)

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

		testMethods := []string{"POST", "PUT", "GET", "DELETE"}

		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, fakeData)

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
			Data:      fakeBodyObject,
		}).Return(nil)

		testAPI.actor = mockActors

		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			mockActors.Calls = nil

			// act
			resp := fakeServer.DoRequest(method, apiPath, fakeData)

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
			Data:      string(base64Encoded),
		}).Return(nil)

		testAPI.actor = mockActors

		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			mockActors.Calls = nil

			// act
			resp := fakeServer.DoRequest(method, apiPath, serializedByteArray)

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
			Data:      expectedObj,
		}).Return(nil)

		testAPI.actor = mockActors

		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			mockActors.Calls = nil

			// act
			resp := fakeServer.DoRequest(method, apiPath, serializedByteArray)

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
			Data:      nonJSONFakeData,
		}).Return(nil)

		testAPI.actor = mockActors

		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			mockActors.Calls = nil

			// act
			resp := fakeServer.DoRequest(method, apiPath, nonJSONFakeData)

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
		resp := fakeServer.DoRequest("GET", apiPath, nil)

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

		testAPI.actor = mockActors

		// act
		resp := fakeServer.DoRequest("DELETE", apiPath, nil)

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
					"key":  "fakeKey1",
					"data": fakeBodyObject,
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

		testAPI.actor = mockActors

		// act
		inputBodyBytes, err := json.Marshal(testTransactionalOperations)

		assert.NoError(t, err)
		resp := fakeServer.DoRequest("POST", apiPath, inputBodyBytes)

		// assert
		assert.Equal(t, 201, resp.StatusCode)
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

func (f *fakeHTTPServer) DoRequest(method, path string, body []byte) fakeHTTPResponse {
	r, _ := gohttp.NewRequest(method, fmt.Sprintf("http://localhost/%s", path), bytes.NewBuffer(body))
	r.Header.Set("Content-Type", "application/json")
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
