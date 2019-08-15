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

	"github.com/valyala/fasthttp/fasthttputil"

	"github.com/actionscore/actions/pkg/actors"
	"github.com/actionscore/actions/pkg/channel"
	"github.com/actionscore/actions/pkg/channel/http"
	"github.com/actionscore/actions/pkg/messaging"
	actionst "github.com/actionscore/actions/pkg/testing"
	routing "github.com/qiangxue/fasthttp-routing"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

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

func TestSetHeaders(t *testing.T) {
	testAPI := &api{directMessaging: mockDirectMessaging{appChannel: mockChannel{}}}
	c := &routing.Context{}
	request := fasthttp.Request{}
	c.RequestCtx = &fasthttp.RequestCtx{Request: request}
	c.Request.Header.Set("H1", "v1")
	c.Request.Header.Set("H2", "v2")
	m := map[string]string{}
	testAPI.setHeaders(c, m)
	assert.Equal(t, "H1&__header_equals__&v1&__header_delim__&H2&__header_equals__&v2", m["headers"])
}

func TestSaveActorState(t *testing.T) {
	testPath := "v1.0/actors/fakeActorType/fakeActorID/state/key1"
	fakeData := []byte("fakeData")

	fakeServer := newFakeHTTPServer()
	testAPI := &api{actor: nil}

	fakeServer.StartServer(testAPI.constructActorEndpoints())

	t.Run("Actor runtime is not initialized", func(t *testing.T) {
		// act
		statusCode, body := fakeServer.DoRequest("PUT", testPath, fakeData)

		// assert
		var bodyObj map[string]string
		json.Unmarshal(body, &bodyObj)
		assert.Equal(t, 400, statusCode)
		assert.Equal(t, "actor runtime is not initialized", bodyObj["error"])
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
	testPath := "v1.0/actors/fakeActorType/fakeActorID/state/key1"
	fakeServer := newFakeHTTPServer()
	testAPI := &api{actor: nil}

	fakeServer.StartServer(testAPI.constructActorEndpoints())

	t.Run("Actor runtime is not initialized", func(t *testing.T) {
		// act
		statusCode, body := fakeServer.DoRequest("GET", testPath, nil)

		// assert
		var bodyObj map[string]string
		json.Unmarshal(body, &bodyObj)
		assert.Equal(t, 400, statusCode)
		assert.Equal(t, "actor runtime is not initialized", bodyObj["error"])
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
func newFakeHTTPServer() *fakeHTTPServer {
	return &fakeHTTPServer{}
}

type fakeHTTPServer struct {
	ln     *fasthttputil.InmemoryListener
	client gohttp.Client
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

func (f *fakeHTTPServer) DoRequest(method, path string, body []byte) (int, []byte) {
	r, _ := gohttp.NewRequest(method, fmt.Sprintf("http://localhost/%s", path), bytes.NewBuffer(body))
	res, err := f.client.Do(r)
	if err != nil {
		panic(fmt.Errorf("failed to request: %v", err))
	}

	bodyBytes, _ := ioutil.ReadAll(res.Body)
	return res.StatusCode, bodyBytes
}
