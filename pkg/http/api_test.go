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
	"strings"
	"testing"

	jsoniter "github.com/json-iterator/go"
	"github.com/valyala/fasthttp/fasthttputil"

	"github.com/actionscore/actions/pkg/actors"
	"github.com/actionscore/actions/pkg/channel/http"
	"github.com/actionscore/actions/pkg/messaging"
	actionst "github.com/actionscore/actions/pkg/testing"
	routing "github.com/qiangxue/fasthttp-routing"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
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
			assert.Equal(t, "ERR_INVOKE_OUTPUT_BINDING", fakeServer.UnmarshalBody(resp.Body)["errorCode"])
		}
	})

	fakeServer.Shutdown()
}

func TestV1DirectMessagingEndpoints(t *testing.T) {
	fakeHeader := "Host&__header_equals__&localhost&__header_delim__&Content-Length&__header_equals__&8&__header_delim__&User-Agent&__header_equals__&Go-http-client/1.1&__header_delim__&Accept-Encoding&__header_equals__&gzip"
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
	testAPI := &api{actor: nil}

	fakeServer.StartServer(testAPI.constructActorEndpoints())

	apiPath := "v1.0/actors/fakeActorType/fakeActorID/state/key1"
	fakeData := []byte("fakeData")

	t.Run("Actor runtime is not initialized", func(t *testing.T) {
		testAPI.actor = nil

		testMethods := []string{"POST", "PUT", "GET", "DELETE"}

		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, fakeData)

			// assert
			assert.Equal(t, 400, resp.StatusCode)
			assert.Equal(t, "ERR_ACTOR_RUNTIME_NOT_FOUND", fakeServer.UnmarshalBody(resp.Body)["errorCode"])
		}
	})

	t.Run("Save actor state - 200 OK", func(t *testing.T) {
		mockActors := new(actionst.MockActors)
		var val interface{}
		jsoniter.ConfigFastest.Unmarshal([]byte(fakeData), &val)
		mockActors.On("SaveState", &actors.SaveStateRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
			Key:       "key1",
			Data:      val,
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

	t.Run("Get actor state - 200 OK", func(t *testing.T) {
		mockActors := new(actionst.MockActors)
		mockActors.On("GetState", &actors.GetStateRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
			Key:       "key1",
		}).Return(&actors.StateResponse{
			Data: []byte("fakeData"),
		}, nil)

		testAPI.actor = mockActors

		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil)

		// assert
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, []byte("fakeData"), resp.Body)
		mockActors.AssertNumberOfCalls(t, "GetState", 1)
	})

	t.Run("Delete actor state - 200 OK", func(t *testing.T) {
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
		assert.Equal(t, 201, resp.StatusCode)
		mockActors.AssertNumberOfCalls(t, "DeleteState", 1)
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
	StatusCode int
	Header     gohttp.Header
	Body       []byte
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
	res, err := f.client.Do(r)
	if err != nil {
		panic(fmt.Errorf("failed to request: %v", err))
	}

	bodyBytes, _ := ioutil.ReadAll(res.Body)

	return fakeHTTPResponse{
		StatusCode: res.StatusCode,
		Header:     res.Header,
		Body:       bodyBytes,
	}
}

func (f *fakeHTTPServer) UnmarshalBody(respBody []byte) map[string]string {
	var bodyObj map[string]string
	json.Unmarshal(respBody, &bodyObj)
	return bodyObj
}
