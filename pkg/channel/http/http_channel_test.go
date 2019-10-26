// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/valyala/fasthttp"

	"github.com/dapr/dapr/pkg/channel"
	"github.com/stretchr/testify/assert"
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

type testContentTypeHandler struct {
	ContentType string
}

func (t *testContentTypeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t.ContentType = r.Header.Get("Content-Type")
	w.Write([]byte(""))
}

type testHandler struct {
}

func (t *testHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	io.WriteString(w, r.URL.RawQuery)
}

type testHandlerHeaders struct {
}

func (t *testHandlerHeaders) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	headers := []string{}
	for k, v := range r.Header {
		headers = append(headers, fmt.Sprintf("%s&__header_equals__&%s", k, v[0]))
	}
	io.WriteString(w, strings.Join(headers, "&__header_delim__&"))
}

func TestInvokeMethod(t *testing.T) {
	server := httptest.NewServer(&testHandler{})
	c := Channel{baseAddress: server.URL, client: &fasthttp.Client{}}
	request := &channel.InvokeRequest{
		Metadata: map[string]string{QueryString: "param1=val1&param2=val2"},
	}
	response, err := c.InvokeMethod(request)
	assert.NoError(t, err)
	assert.Equal(t, "param1=val1&param2=val2", string(response.Data))
	server.Close()
}

func TestInvokeMethodMaxConcurrency(t *testing.T) {
	t.Run("single concurrency", func(t *testing.T) {
		handler := testConcurrencyHandler{
			maxCalls: 1,
			lock:     sync.Mutex{},
		}
		server := httptest.NewServer(&handler)
		c := Channel{baseAddress: server.URL, client: &fasthttp.Client{}}
		c.ch = make(chan int, 1)

		var wg sync.WaitGroup
		wg.Add(5)
		for i := 0; i < 5; i++ {
			go func() {
				request2 := &channel.InvokeRequest{
					Payload: []byte(""),
				}
				c.InvokeMethod(request2)
				wg.Done()
			}()
		}
		wg.Wait()
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

		var wg sync.WaitGroup
		wg.Add(20)
		for i := 0; i < 20; i++ {
			go func() {
				request2 := &channel.InvokeRequest{
					Payload: []byte(""),
				}
				c.InvokeMethod(request2)
				wg.Done()
			}()
		}
		wg.Wait()
		assert.False(t, handler.testFailed)
		server.Close()
	})
}

func TestInvokeWithHeaders(t *testing.T) {
	server := httptest.NewServer(&testHandlerHeaders{})
	c := Channel{baseAddress: server.URL, client: &fasthttp.Client{}}
	request := &channel.InvokeRequest{
		Metadata: map[string]string{
			"headers": "h1&__header_equals__&v1&__header_delim__&h2&__header_equals__&v2",
		},
	}
	response, err := c.InvokeMethod(request)
	assert.NoError(t, err)
	assert.Contains(t, string(response.Data), "H1&__header_equals__&v1")
	assert.Contains(t, string(response.Data), "H2&__header_equals__&v2")
	server.Close()
}

func TestContentType(t *testing.T) {
	t.Run("default application/json", func(t *testing.T) {
		handler := &testContentTypeHandler{}
		server := httptest.NewServer(handler)
		c := Channel{baseAddress: server.URL, client: &fasthttp.Client{}}
		request := &channel.InvokeRequest{}
		c.InvokeMethod(request)
		assert.Equal(t, "application/json", handler.ContentType)
		server.Close()
	})

	t.Run("application/json", func(t *testing.T) {
		handler := &testContentTypeHandler{}
		server := httptest.NewServer(handler)
		c := Channel{baseAddress: server.URL, client: &fasthttp.Client{}}
		request := &channel.InvokeRequest{
			Metadata: map[string]string{ContentType: "application/json"},
		}
		c.InvokeMethod(request)
		assert.Equal(t, "application/json", handler.ContentType)
		server.Close()
	})

	t.Run("custom", func(t *testing.T) {
		handler := &testContentTypeHandler{}
		server := httptest.NewServer(handler)
		c := Channel{baseAddress: server.URL, client: &fasthttp.Client{}}
		request := &channel.InvokeRequest{
			Metadata: map[string]string{ContentType: "custom"},
		}
		c.InvokeMethod(request)
		assert.Equal(t, "custom", handler.ContentType)
		server.Close()
	})
}
