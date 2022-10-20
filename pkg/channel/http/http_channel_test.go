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

package http

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
	"go.uber.org/atomic"

	"github.com/dapr/dapr/pkg/config"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	httpMiddleware "github.com/dapr/dapr/pkg/middleware/http"
)

// testConcurrencyHandler is used for testing max concurrency.
type testConcurrencyHandler struct {
	maxCalls     int32
	currentCalls *atomic.Int32
	testFailed   bool
}

func (t *testConcurrencyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cur := t.currentCalls.Inc()

	if cur > t.maxCalls {
		t.testFailed = true
	}

	t.currentCalls.Dec()
	io.WriteString(w, r.URL.RawQuery)
}

type testContentTypeHandler struct{}

func (t *testContentTypeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	io.WriteString(w, r.Header.Get("Content-Type"))
}

type testHandlerHeaders struct{}

func (t *testHandlerHeaders) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	headers := map[string]string{}
	for k, v := range r.Header {
		headers[k] = v[0]
	}
	rsp, _ := json.Marshal(headers)
	io.WriteString(w, string(rsp))
}

// testHTTPHandler is used for querystring test.
type testHTTPHandler struct {
	serverURL string

	t *testing.T
}

func (th *testHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	assert.Equal(th.t, th.serverURL, r.Host)
	io.WriteString(w, r.URL.RawQuery)
}

// testStatusCodeHandler is used to send responses with a given status code.
type testStatusCodeHandler struct {
	Code int
}

func (t *testStatusCodeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	code, err := strconv.Atoi(r.Header.Get("x-response-status"))
	if err != nil || code == 0 {
		code = t.Code
		if code == 0 {
			code = 200
		}
	}
	w.WriteHeader(code)
	w.Write([]byte(strconv.Itoa(code)))
}

func TestInvokeMethodMiddlewaresPipeline(t *testing.T) {
	defaultStatusCode := http.StatusOK
	th := &testStatusCodeHandler{Code: defaultStatusCode}
	server := httptest.NewServer(th)
	ctx := context.Background()

	t.Run("pipeline should be called when handlers are not empty", func(t *testing.T) {
		called := 0
		middleware := httpMiddleware.Middleware(func(h fasthttp.RequestHandler) fasthttp.RequestHandler {
			return func(ctx *fasthttp.RequestCtx) {
				called++
				h(ctx)
			}
		})
		pipeline := httpMiddleware.Pipeline{
			Handlers: []httpMiddleware.Middleware{
				middleware,
			},
		}
		c := Channel{
			baseAddress: server.URL,
			client:      &fasthttp.Client{},
			pipeline:    pipeline,
		}
		fakeReq := invokev1.NewInvokeMethodRequest("method")
		fakeReq.WithHTTPExtension(http.MethodPost, "param1=val1&param2=val2")

		// act
		resp, err := c.InvokeMethod(ctx, fakeReq)

		// assert
		assert.NoError(t, err)
		assert.Equal(t, 1, called)
		assert.Equal(t, int32(defaultStatusCode), resp.Status().Code)
	})

	t.Run("request can be short-circuited by middleware pipeline", func(t *testing.T) {
		called := 0
		shortcircuitStatusCode := http.StatusBadGateway
		middleware := httpMiddleware.Middleware(func(h fasthttp.RequestHandler) fasthttp.RequestHandler {
			return func(ctx *fasthttp.RequestCtx) {
				called++
				ctx.Response.SetStatusCode(shortcircuitStatusCode)
			}
		})
		pipeline := httpMiddleware.Pipeline{
			Handlers: []httpMiddleware.Middleware{
				middleware,
			},
		}
		c := Channel{
			baseAddress: server.URL,
			client:      &fasthttp.Client{},
			pipeline:    pipeline,
		}
		fakeReq := invokev1.NewInvokeMethodRequest("method")
		fakeReq.WithHTTPExtension(http.MethodPost, "param1=val1&param2=val2")

		// act
		resp, err := c.InvokeMethod(ctx, fakeReq)

		// assert
		assert.NoError(t, err)
		assert.Equal(t, 1, called)
		assert.Equal(t, int32(shortcircuitStatusCode), resp.Status().Code)
	})

	server.Close()
}

func TestInvokeMethod(t *testing.T) {
	th := &testHTTPHandler{t: t, serverURL: ""}
	server := httptest.NewServer(th)
	ctx := context.Background()

	t.Run("query string", func(t *testing.T) {
		c := Channel{
			baseAddress: server.URL,
			client:      &fasthttp.Client{},
			tracingSpec: config.TracingSpec{
				SamplingRate: "0",
			},
		}
		th.serverURL = server.URL[len("http://"):]
		fakeReq := invokev1.NewInvokeMethodRequest("method")
		fakeReq.WithHTTPExtension(http.MethodPost, "param1=val1&param2=val2")

		// act
		response, err := c.InvokeMethod(ctx, fakeReq)

		// assert
		assert.NoError(t, err)
		_, body := response.RawData()
		assert.Equal(t, "param1=val1&param2=val2", string(body))
	})

	t.Run("tracing is enabled", func(t *testing.T) {
		c := Channel{
			baseAddress: server.URL,
			client:      &fasthttp.Client{},
			tracingSpec: config.TracingSpec{
				SamplingRate: "1",
			},
		}
		th.serverURL = server.URL[len("http://"):]
		fakeReq := invokev1.NewInvokeMethodRequest("method")
		fakeReq.WithHTTPExtension(http.MethodPost, "")

		// act
		response, err := c.InvokeMethod(ctx, fakeReq)

		// assert
		assert.NoError(t, err)
		_, body := response.RawData()
		assert.Equal(t, "", string(body))
	})

	server.Close()
}

func TestInvokeMethodMaxConcurrency(t *testing.T) {
	ctx := context.Background()
	t.Run("single concurrency", func(t *testing.T) {
		handler := testConcurrencyHandler{
			maxCalls:     1,
			currentCalls: atomic.NewInt32(0),
		}
		server := httptest.NewServer(&handler)
		c := Channel{
			baseAddress: server.URL,
			client:      &fasthttp.Client{},
			ch:          make(chan struct{}, 1),
		}

		// act
		var wg sync.WaitGroup
		wg.Add(5)
		for i := 0; i < 5; i++ {
			go func() {
				req := invokev1.
					NewInvokeMethodRequest("method").
					WithHTTPExtension("GET", "").
					WithRawData(nil, "")
				_, err := c.InvokeMethod(ctx, req)
				assert.NoError(t, err)
				wg.Done()
			}()
		}
		wg.Wait()

		// assert
		assert.False(t, handler.testFailed)
		server.Close()
	})

	t.Run("10 concurrent calls", func(t *testing.T) {
		handler := testConcurrencyHandler{
			maxCalls:     10,
			currentCalls: atomic.NewInt32(0),
		}
		server := httptest.NewServer(&handler)
		c := Channel{
			baseAddress: server.URL,
			client:      &fasthttp.Client{},
			ch:          make(chan struct{}, 1),
		}

		// act
		var wg sync.WaitGroup
		wg.Add(20)
		for i := 0; i < 20; i++ {
			go func() {
				req := invokev1.
					NewInvokeMethodRequest("method").
					WithHTTPExtension("GET", "").
					WithRawData(nil, "")
				_, err := c.InvokeMethod(ctx, req)
				assert.NoError(t, err)
				wg.Done()
			}()
		}
		wg.Wait()

		// assert
		assert.False(t, handler.testFailed)
		server.Close()
	})

	t.Run("introduce failures", func(t *testing.T) {
		handler := testConcurrencyHandler{
			maxCalls:     5,
			currentCalls: atomic.NewInt32(0),
		}
		server := httptest.NewServer(&handler)
		c := Channel{
			// False address to make first calls fail
			baseAddress: "http://0.0.0.0:0",
			client:      &fasthttp.Client{},
			ch:          make(chan struct{}, 1),
		}

		// act
		for i := 0; i < 20; i++ {
			if i == 10 {
				c.baseAddress = server.URL
			}
			req := invokev1.
				NewInvokeMethodRequest("method").
				WithHTTPExtension("GET", "").
				WithRawData(nil, "")
			_, err := c.InvokeMethod(ctx, req)
			if i < 10 {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		}

		// assert
		assert.False(t, handler.testFailed)
		server.Close()
	})
}

func TestInvokeWithHeaders(t *testing.T) {
	ctx := context.Background()
	testServer := httptest.NewServer(&testHandlerHeaders{})
	c := Channel{baseAddress: testServer.URL, client: &fasthttp.Client{}}

	req := invokev1.NewInvokeMethodRequest("method")
	md := map[string][]string{
		"H1": {"v1"},
		"H2": {"v2"},
	}
	req.WithMetadata(md)
	req.WithHTTPExtension(http.MethodPost, "")

	// act
	response, err := c.InvokeMethod(ctx, req)

	// assert
	assert.NoError(t, err)
	_, body := response.RawData()

	actual := map[string]string{}
	json.Unmarshal(body, &actual)

	assert.NoError(t, err)
	assert.Contains(t, "v1", actual["H1"])
	assert.Contains(t, "v2", actual["H2"])
	testServer.Close()
}

func TestContentType(t *testing.T) {
	ctx := context.Background()

	t.Run("no default content type", func(t *testing.T) {
		handler := &testContentTypeHandler{}
		testServer := httptest.NewServer(handler)
		c := Channel{
			baseAddress: testServer.URL,
			client:      &fasthttp.Client{},
		}
		req := invokev1.NewInvokeMethodRequest("method")
		req.WithRawData(nil, "")
		req.WithHTTPExtension(http.MethodGet, "")

		// act
		resp, err := c.InvokeMethod(ctx, req)

		// assert
		assert.NoError(t, err)
		contentType, body := resp.RawData()
		assert.Equal(t, "", contentType)
		assert.Equal(t, []byte{}, body)
		testServer.Close()
	})

	t.Run("application/json", func(t *testing.T) {
		handler := &testContentTypeHandler{}
		testServer := httptest.NewServer(handler)
		c := Channel{baseAddress: testServer.URL, client: &fasthttp.Client{}}
		req := invokev1.NewInvokeMethodRequest("method")
		req.WithRawData(nil, "application/json")
		req.WithHTTPExtension(http.MethodPost, "")

		// act
		resp, err := c.InvokeMethod(ctx, req)

		// assert
		assert.NoError(t, err)
		contentType, body := resp.RawData()
		assert.Equal(t, "text/plain; charset=utf-8", contentType)
		assert.Equal(t, []byte("application/json"), body)
		testServer.Close()
	})

	t.Run("text/plain", func(t *testing.T) {
		handler := &testContentTypeHandler{}
		testServer := httptest.NewServer(handler)
		c := Channel{baseAddress: testServer.URL, client: &fasthttp.Client{}}
		req := invokev1.NewInvokeMethodRequest("method")
		req.WithRawData(nil, "text/plain")
		req.WithHTTPExtension(http.MethodPost, "")

		// act
		resp, err := c.InvokeMethod(ctx, req)

		// assert
		assert.NoError(t, err)
		contentType, body := resp.RawData()
		assert.Equal(t, "text/plain; charset=utf-8", contentType)
		assert.Equal(t, []byte("text/plain"), body)
		testServer.Close()
	})
}

func TestAppToken(t *testing.T) {
	t.Run("token present", func(t *testing.T) {
		ctx := context.Background()
		testServer := httptest.NewServer(&testHandlerHeaders{})
		c := Channel{baseAddress: testServer.URL, client: &fasthttp.Client{}, appHeaderToken: "token1"}

		req := invokev1.NewInvokeMethodRequest("method")
		req.WithHTTPExtension(http.MethodPost, "")

		// act
		response, err := c.InvokeMethod(ctx, req)

		// assert
		assert.NoError(t, err)
		_, body := response.RawData()

		actual := map[string]string{}
		json.Unmarshal(body, &actual)

		_, hasToken := actual["Dapr-Api-Token"]
		assert.NoError(t, err)
		assert.True(t, hasToken)
		testServer.Close()
	})

	t.Run("token not present", func(t *testing.T) {
		ctx := context.Background()
		testServer := httptest.NewServer(&testHandlerHeaders{})
		c := Channel{baseAddress: testServer.URL, client: &fasthttp.Client{}}

		req := invokev1.NewInvokeMethodRequest("method")
		req.WithHTTPExtension(http.MethodPost, "")

		// act
		response, err := c.InvokeMethod(ctx, req)

		// assert
		assert.NoError(t, err)
		_, body := response.RawData()

		actual := map[string]string{}
		json.Unmarshal(body, &actual)

		_, hasToken := actual["Dapr-Api-Token"]
		assert.NoError(t, err)
		assert.False(t, hasToken)
		testServer.Close()
	})
}

func TestCreateChannel(t *testing.T) {
	t.Run("ssl scheme", func(t *testing.T) {
		ch, err := CreateLocalChannel(3000, 0, httpMiddleware.Pipeline{}, config.TracingSpec{}, true, 4, 4)
		assert.NoError(t, err)

		b := ch.GetBaseAddress()
		assert.Equal(t, b, "https://127.0.0.1:3000")
	})

	t.Run("non-ssl scheme", func(t *testing.T) {
		ch, err := CreateLocalChannel(3000, 0, httpMiddleware.Pipeline{}, config.TracingSpec{}, false, 4, 4)
		assert.NoError(t, err)

		b := ch.GetBaseAddress()
		assert.Equal(t, b, "http://127.0.0.1:3000")
	})
}

func TestHealthProbe(t *testing.T) {
	ctx := context.Background()
	h := &testStatusCodeHandler{}
	testServer := httptest.NewServer(h)
	c := Channel{baseAddress: testServer.URL, client: &fasthttp.Client{}}

	var (
		success bool
		err     error
	)

	// OK response
	success, err = c.HealthProbe(ctx)
	assert.NoError(t, err)
	assert.True(t, success)

	// Non-2xx status code
	h.Code = 500
	success, err = c.HealthProbe(ctx)
	assert.NoError(t, err)
	assert.False(t, success)

	// Stopped server
	// Should still return no error, but a failed probe
	testServer.Close()
	success, err = c.HealthProbe(ctx)
	assert.NoError(t, err)
	assert.False(t, success)
}
