package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/actionscore/actions/pkg/actors"
	"github.com/actionscore/actions/pkg/channel"
	"github.com/actionscore/actions/pkg/channel/http"
	"github.com/actionscore/actions/pkg/messaging"
	actionst "github.com/actionscore/actions/pkg/testing"
	routing "github.com/qiangxue/fasthttp-routing"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

const DefaultFakeServerPort = 30000

type mockChannel struct {
}

func (c mockChannel) InvokeMethod(req *channel.InvokeRequest) (*channel.InvokeResponse, error) {
	if val, ok := req.Metadata[http.QueryString]; ok {
		return &channel.InvokeResponse{Data: []byte(val)}, nil
	}
	return nil, nil
}

type mockDirectMessaging struct {
	appChannel channel.AppChannel
}

func (d mockDirectMessaging) Invoke(req *messaging.DirectMessageRequest) (*messaging.DirectMessageResponse, error) {
	localInvokeReq := channel.InvokeRequest{
		Metadata: req.Metadata,
		Method:   req.Method,
		Payload:  req.Data,
	}

	resp, err := d.appChannel.InvokeMethod(&localInvokeReq)
	if err != nil {
		return nil, err
	}

	return &messaging.DirectMessageResponse{
		Data:     resp.Data,
		Metadata: resp.Metadata,
	}, nil
}

func TestOutputBinding(t *testing.T) {
	t.Run("with correct input params", func(t *testing.T) {
		testAPI := &api{directMessaging: mockDirectMessaging{appChannel: mockChannel{}}, sendToOutputBindingFn: func(name string, data []byte) error {
			return nil
		}}
		c := &routing.Context{}
		request := fasthttp.Request{}
		request.URI().Parse(nil, []byte("http://actionscore.dev/bindings/test"))
		c.RequestCtx = &fasthttp.RequestCtx{Request: request}
		err := testAPI.onOutputBindingMessage(c)
		assert.NoError(t, err)
		assert.Equal(t, 200, c.Response.StatusCode())
	})

	t.Run("with missing binding name", func(t *testing.T) {
		testAPI := &api{directMessaging: mockDirectMessaging{appChannel: mockChannel{}}, sendToOutputBindingFn: func(name string, data []byte) error {
			return errors.New("missing binding name")
		}}
		c := &routing.Context{}
		request := fasthttp.Request{}
		request.URI().Parse(nil, []byte("http://actionscore.dev/bindings/test"))
		c.RequestCtx = &fasthttp.RequestCtx{Request: request}
		testAPI.onOutputBindingMessage(c)
		assert.NotEqual(t, 200, c.Response.StatusCode())
	})
}

func TestOnDirectMessage(t *testing.T) {
	t.Run("with parameters", func(t *testing.T) {
		testAPI := &api{directMessaging: mockDirectMessaging{appChannel: mockChannel{}}}
		c := &routing.Context{}
		request := fasthttp.Request{}
		request.URI().Parse(nil, []byte("http://www.microsoft.com/dummy?param1=val1&param2=val2"))
		c.RequestCtx = &fasthttp.RequestCtx{Request: request}
		err := testAPI.onDirectMessage(c)
		assert.NoError(t, err)
		assert.Equal(t, "param1=val1&param2=val2", string(c.Response.Body()))
	})

	t.Run("without parameters", func(t *testing.T) {
		testAPI := &api{directMessaging: mockDirectMessaging{appChannel: mockChannel{}}}
		c := &routing.Context{}
		request := fasthttp.Request{}
		request.URI().Parse(nil, []byte("http://www.microsoft.com/dummy"))
		c.RequestCtx = &fasthttp.RequestCtx{Request: request}
		err := testAPI.onDirectMessage(c)
		assert.NoError(t, err)
		assert.Equal(t, "", string(c.Response.Body()))
	})
}

func TestSaveActorState(t *testing.T) {
	testPath := "v1.0/actors/fakeActorType/fakeActorID/states/key1"
	fakeData := []byte("fakeData")

	fakeServer := &fakeHTTPServer{Port: DefaultFakeServerPort}
	testAPI := &api{actor: nil}

	fakeServer.StartServer(testAPI.constructActorEndpoints())

	t.Run("Actor is not initialized", func(t *testing.T) {
		// act
		statusCode, body := fakeServer.DoRequest("PUT", testPath, fakeData)

		// assert
		var bodyObj map[string]string
		json.Unmarshal(body, &bodyObj)
		assert.Equal(t, 400, statusCode)
		assert.Equal(t, "actor is not initialized", bodyObj["error"])
	})

	t.Run("Save granular state key", func(t *testing.T) {
		mockActors := new(actionst.MockActors)
		mockActors.On("SaveState", &actors.SaveStateRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
			Key:       "key1",
			Data:      fakeData,
		}).Return(nil)

		testAPI.actor = mockActors

		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			statusCode, _ := fakeServer.DoRequest(method, testPath, fakeData)

			// assert
			assert.Equal(t, 201, statusCode, "failed to save state key with %s", method)
		}
	})

	fakeServer.Shutdown()
}

func TestGetActorState(t *testing.T) {
	testPath := "v1.0/actors/fakeActorType/fakeActorID/states/key1"
	fakeServer := &fakeHTTPServer{Port: DefaultFakeServerPort}
	testAPI := &api{actor: nil}

	fakeServer.StartServer(testAPI.constructActorEndpoints())

	t.Run("Actor is not initialized", func(t *testing.T) {
		// act
		statusCode, body := fakeServer.DoRequest("GET", testPath, nil)

		// assert
		var bodyObj map[string]string
		json.Unmarshal(body, &bodyObj)
		assert.Equal(t, 400, statusCode)
		assert.Equal(t, "actor is not initialized", bodyObj["error"])
	})

	t.Run("Get Actor State successfully", func(t *testing.T) {
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
		statusCode, body := fakeServer.DoRequest("GET", testPath, nil)

		// assert
		assert.Equal(t, 200, statusCode)
		assert.Equal(t, []byte("fakeData"), body)
	})

	fakeServer.Shutdown()
}

// Fake http server and client helpers to simplify endpoints test
type fakeHTTPServer struct {
	Port   int
	router *routing.Router
	server fasthttp.Server
}

func (f *fakeHTTPServer) StartServer(endpoints []Endpoint) {
	f.router = f.getRouter(endpoints)
	f.server = fasthttp.Server{
		Handler:     f.router.HandleRequest,
		ReadTimeout: 1 * time.Second,
	}

	go func() {
		f.server.ListenAndServe(fmt.Sprintf(":%d", f.Port))
	}()

	// Sleep until fake server is fully initialized
	time.Sleep(1 * time.Second)
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
	f.server.Shutdown()
}

func (f *fakeHTTPServer) DoRequest(method, path string, body []byte) (int, []byte) {
	req := fasthttp.AcquireRequest()
	req.SetRequestURI(fmt.Sprintf("http://localhost:%d/%s", f.Port, path))
	req.SetBody(body)
	req.Header.SetMethod(method)

	resp := fasthttp.AcquireResponse()
	client := &fasthttp.Client{}
	client.Do(req, resp)

	return resp.StatusCode(), resp.Body()
}
