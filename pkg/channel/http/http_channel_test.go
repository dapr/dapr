// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"

	"github.com/dapr/dapr/pkg/config"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
)

type testConcurrencyHandler struct {
	maxCalls     int
	currentCalls int
	testFailed   bool
	lock         sync.Mutex
}

func (t *testConcurrencyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t.lock.Lock()
	t.currentCalls++
	t.lock.Unlock()

	if t.currentCalls > t.maxCalls {
		t.testFailed = true
	}

	t.lock.Lock()
	t.currentCalls--
	t.lock.Unlock()
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
			maxCalls: 1,
			lock:     sync.Mutex{},
		}
		server := httptest.NewServer(&handler)
		c := Channel{baseAddress: server.URL, client: &fasthttp.Client{}}
		c.ch = make(chan int, 1)

		// act
		var wg sync.WaitGroup
		wg.Add(5)
		for i := 0; i < 5; i++ {
			go func() {
				request2 := invokev1.NewInvokeMethodRequest("method")
				request2.WithRawData(nil, "")

				c.InvokeMethod(ctx, request2)
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
			maxCalls: 10,
			lock:     sync.Mutex{},
		}
		server := httptest.NewServer(&handler)
		c := Channel{baseAddress: server.URL, client: &fasthttp.Client{}}
		c.ch = make(chan int, 1)

		// act
		var wg sync.WaitGroup
		wg.Add(20)
		for i := 0; i < 20; i++ {
			go func() {
				request2 := invokev1.NewInvokeMethodRequest("method")
				request2.WithRawData(nil, "")
				c.InvokeMethod(ctx, request2)
				wg.Done()
			}()
		}
		wg.Wait()

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
	t.Run("default application/json", func(t *testing.T) {
		handler := &testContentTypeHandler{}
		testServer := httptest.NewServer(handler)
		c := Channel{baseAddress: testServer.URL, client: &fasthttp.Client{}}
		req := invokev1.NewInvokeMethodRequest("method")
		req.WithRawData(nil, "")
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
		ch, err := CreateLocalChannel(3000, 0, config.TracingSpec{}, true, 4, 4)
		assert.NoError(t, err)

		b := ch.GetBaseAddress()
		assert.Equal(t, b, "https://127.0.0.1:3000")
	})

	t.Run("non-ssl scheme", func(t *testing.T) {
		ch, err := CreateLocalChannel(3000, 0, config.TracingSpec{}, false, 4, 4)
		assert.NoError(t, err)

		b := ch.GetBaseAddress()
		assert.Equal(t, b, "http://127.0.0.1:3000")
	})
}
