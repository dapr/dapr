/*
Copyright 2022 The Dapr Authors
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

package nethttpadaptor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

func TestNewNetHTTPHandlerFuncRequests(t *testing.T) {
	tests := []struct {
		name                string
		inputRequestFactory func() *http.Request
		evaluateFactory     func(t *testing.T) func(ctx *fasthttp.RequestCtx)
	}{
		{
			"Get method is handled",
			func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "http://localhost:8080/test", nil)
			},
			func(t *testing.T) func(ctx *fasthttp.RequestCtx) {
				return func(ctx *fasthttp.RequestCtx) {
					assert.Equal(t, http.MethodGet, string(ctx.Method()))
				}
			},
		},
		{
			"Post method is handled",
			func() *http.Request {
				return httptest.NewRequest(http.MethodPost, "http://localhost:8080/test", nil)
			},
			func(t *testing.T) func(ctx *fasthttp.RequestCtx) {
				return func(ctx *fasthttp.RequestCtx) {
					assert.Equal(t, http.MethodPost, string(ctx.Method()))
				}
			},
		},
		{
			"Put method is handled",
			func() *http.Request {
				return httptest.NewRequest(http.MethodPut, "http://localhost:8080/test", nil)
			},
			func(t *testing.T) func(ctx *fasthttp.RequestCtx) {
				return func(ctx *fasthttp.RequestCtx) {
					assert.Equal(t, http.MethodPut, string(ctx.Method()))
				}
			},
		},
		{
			"Options method is handled",
			func() *http.Request {
				return httptest.NewRequest(http.MethodOptions, "http://localhost:8080/test", nil)
			},
			func(t *testing.T) func(ctx *fasthttp.RequestCtx) {
				return func(ctx *fasthttp.RequestCtx) {
					assert.Equal(t, http.MethodOptions, string(ctx.Method()))
				}
			},
		},
		{
			"Patch method is handled",
			func() *http.Request {
				return httptest.NewRequest(http.MethodPatch, "http://localhost:8080/test", nil)
			},
			func(t *testing.T) func(ctx *fasthttp.RequestCtx) {
				return func(ctx *fasthttp.RequestCtx) {
					assert.Equal(t, http.MethodPatch, string(ctx.Method()))
				}
			},
		},
		{
			"Delete method is handled",
			func() *http.Request {
				return httptest.NewRequest(http.MethodDelete, "http://localhost:8080/test", nil)
			},
			func(t *testing.T) func(ctx *fasthttp.RequestCtx) {
				return func(ctx *fasthttp.RequestCtx) {
					assert.Equal(t, http.MethodDelete, string(ctx.Method()))
				}
			},
		},
		{
			"Host is handled",
			func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "http://localhost:8080/test", nil)
			},
			func(t *testing.T) func(ctx *fasthttp.RequestCtx) {
				return func(ctx *fasthttp.RequestCtx) {
					assert.Equal(t, "localhost:8080", string(ctx.Host()))
				}
			},
		},
		{
			"Path is handled",
			func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "http://localhost:8080/test/sub", nil)
			},
			func(t *testing.T) func(ctx *fasthttp.RequestCtx) {
				return func(ctx *fasthttp.RequestCtx) {
					assert.Equal(t, "/test/sub", string(ctx.Path()))
				}
			},
		},
		{
			"Body is handled",
			func() *http.Request {
				body := strings.NewReader("test body!")

				return httptest.NewRequest(http.MethodGet, "http://localhost:8080/test/sub", body)
			},
			func(t *testing.T) func(ctx *fasthttp.RequestCtx) {
				return func(ctx *fasthttp.RequestCtx) {
					assert.Equal(t, "test body!", string(ctx.Request.Body()))
				}
			},
		},
		{
			"Querystring is handled",
			func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "http://localhost:8080/test/sub?alice=bob&version=0.1.2", nil)
			},
			func(t *testing.T) func(ctx *fasthttp.RequestCtx) {
				return func(ctx *fasthttp.RequestCtx) {
					assert.Equal(t, "alice=bob&version=0.1.2", string(ctx.Request.URI().QueryString()))
				}
			},
		},
		{
			"Headers are handled",
			func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/test/sub?alice=bob&version=0.1.2", nil)
				req.Header.Add("testHeaderKey1", "testHeaderValue1")
				req.Header.Add("testHeaderKey2", "testHeaderValue2")
				req.Header.Add("testHeaderKey3", "testHeaderValue3")

				return req
			},
			func(t *testing.T) func(ctx *fasthttp.RequestCtx) {
				return func(ctx *fasthttp.RequestCtx) {
					var header1Found bool
					var header2Found bool
					var header3Found bool
					ctx.Request.Header.VisitAll(func(k []byte, v []byte) {
						switch string(k) {
						case "Testheaderkey1":
							header1Found = true
						case "Testheaderkey2":
							header2Found = true
						case "Testheaderkey3":
							header3Found = true
						}
					})
					assert.True(t, header1Found, "header1Found should be true but is false")
					assert.True(t, header2Found, "header2Found should be true but is false")
					assert.True(t, header3Found, "header3Found should be true but is false")
				}
			},
		},
		{
			"Duplicate headers are handled",
			func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/test/sub?alice=bob&version=0.1.2", nil)
				req.Header.Add("testHeaderKey1", "testHeaderValue1")
				req.Header.Add("testHeaderKey1", "testHeaderValue2")
				req.Header.Add("testHeaderKey1", "testHeaderValue3")

				return req
			},
			func(t *testing.T) func(ctx *fasthttp.RequestCtx) {
				return func(ctx *fasthttp.RequestCtx) {
					var headerValue1Found bool
					var headerValue2Found bool
					var headerValue3Found bool
					ctx.Request.Header.VisitAll(func(k []byte, v []byte) {
						if string(k) == "Testheaderkey1" {
							switch string(v) {
							case "testHeaderValue1":
								headerValue1Found = true
							case "testHeaderValue2":
								headerValue2Found = true
							case "testHeaderValue3":
								headerValue3Found = true
							}
						}
					})
					assert.True(t, headerValue1Found, "headerValue1Found should be true but is false")
					assert.True(t, headerValue2Found, "headerValue2Found should be true but is false")
					assert.True(t, headerValue3Found, "headerValue3Found should be true but is false")
				}
			},
		},
		{
			"Scheme is handled",
			func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "https://localhost:8080", nil)
			},
			func(t *testing.T) func(ctx *fasthttp.RequestCtx) {
				return func(ctx *fasthttp.RequestCtx) {
					assert.Equal(t, "https", string(ctx.Request.URI().Scheme()))
				}
			},
		},
		{
			"Content-Type is handled",
			func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "https://localhost:8080", nil)
				req.Header.Add("Content-Type", "application/json")

				return req
			},
			func(t *testing.T) func(ctx *fasthttp.RequestCtx) {
				return func(ctx *fasthttp.RequestCtx) {
					assert.Equal(t, "application/json", string(ctx.Request.Header.ContentType()))
				}
			},
		},
		{
			"Content-Type with boundary is handled",
			func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "https://localhost:8080", nil)
				req.Header.Add("Content-Type", "multipart/form-data; boundary=test-boundary")

				return req
			},
			func(t *testing.T) func(ctx *fasthttp.RequestCtx) {
				return func(ctx *fasthttp.RequestCtx) {
					assert.Equal(t, "multipart/form-data; boundary=test-boundary", string(ctx.Request.Header.ContentType()))
				}
			},
		},
		{
			"User-Agent is handled",
			func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "https://localhost:8080", nil)
				req.Header.Add("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/51.0.2704.103 Safari/537.36")

				return req
			},
			func(t *testing.T) func(ctx *fasthttp.RequestCtx) {
				return func(ctx *fasthttp.RequestCtx) {
					assert.Equal(t, "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/51.0.2704.103 Safari/537.36", string(ctx.Request.Header.UserAgent()))
				}
			},
		},
		{
			"Referer is handled",
			func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "https://localhost:8080", nil)
				req.Header.Add("Referer", "testReferer")

				return req
			},
			func(t *testing.T) func(ctx *fasthttp.RequestCtx) {
				return func(ctx *fasthttp.RequestCtx) {
					assert.Equal(t, "testReferer", string(ctx.Request.Header.Referer()))
				}
			},
		},
		{
			"BasicAuth is handled",
			func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "https://localhost:8080", nil)
				req.Header.Add("Authorization", "Basic YWxhZGRpbjpvcGVuc2VzYW1l") // b64(aladdin:opensesame)

				return req
			},
			func(t *testing.T) func(ctx *fasthttp.RequestCtx) {
				return func(ctx *fasthttp.RequestCtx) {
					var basicAuth string
					ctx.Request.Header.VisitAll(func(k []byte, v []byte) {
						if string(k) == "Authorization" {
							basicAuth = string(v)
						}
					})
					assert.Equal(t, "Basic YWxhZGRpbjpvcGVuc2VzYW1l", basicAuth)
				}
			},
		},
		{
			"RemoteAddr is handled",
			func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "https://localhost:8080", nil)
				req.RemoteAddr = "1.1.1.1"

				return req
			},
			func(t *testing.T) func(ctx *fasthttp.RequestCtx) {
				return func(ctx *fasthttp.RequestCtx) {
					assert.Equal(t, "1.1.1.1", ctx.RemoteAddr().String())
				}
			},
		},
		{
			"nil body is handled",
			func() *http.Request {
				req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://localhost:8080", nil)

				return req
			},
			func(t *testing.T) func(ctx *fasthttp.RequestCtx) {
				return func(ctx *fasthttp.RequestCtx) {
					assert.Empty(t, ctx.Request.Body())
				}
			},
		},
		{
			"proto headers are handled",
			func() *http.Request {
				req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://localhost:8080", nil)

				return req
			},
			func(t *testing.T) func(ctx *fasthttp.RequestCtx) {
				return func(ctx *fasthttp.RequestCtx) {
					var major, minor string
					ctx.Request.Header.VisitAll(func(k []byte, v []byte) {
						if strings.EqualFold(string(k), "protomajor") {
							major = string(v)
						}
						if strings.EqualFold(string(k), "protominor") {
							minor = string(v)
						}
					})
					assert.Equal(t, "1", major)
					assert.Equal(t, "1", minor)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := tt.inputRequestFactory()
			handler := NewNetHTTPHandlerFunc(tt.evaluateFactory(t))

			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
		})
	}
}

func TestNewNetHTTPHandlerFuncResponses(t *testing.T) {
	tests := []struct {
		name                string
		inputHandlerFactory func() fasthttp.RequestHandler
		inputRequestFactory func() *http.Request
		evaluate            func(t *testing.T, res *http.Response)
	}{
		{
			"200 status code is handled",
			func() fasthttp.RequestHandler {
				return func(ctx *fasthttp.RequestCtx) {
					ctx.SetStatusCode(200)
				}
			},
			func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "http://localhost:8080/test", nil)
			},
			func(t *testing.T, res *http.Response) {
				assert.Equal(t, 200, res.StatusCode)
			},
		},
		{
			"500 status code is handled",
			func() fasthttp.RequestHandler {
				return func(ctx *fasthttp.RequestCtx) {
					ctx.SetStatusCode(500)
				}
			},
			func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "http://localhost:8080/test", nil)
			},
			func(t *testing.T, res *http.Response) {
				assert.Equal(t, 500, res.StatusCode)
			},
		},
		{
			"400 status code is handled",
			func() fasthttp.RequestHandler {
				return func(ctx *fasthttp.RequestCtx) {
					ctx.SetStatusCode(400)
				}
			},
			func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "http://localhost:8080/test", nil)
			},
			func(t *testing.T, res *http.Response) {
				assert.Equal(t, 400, res.StatusCode)
			},
		},
		{
			"Body is handled",
			func() fasthttp.RequestHandler {
				return func(ctx *fasthttp.RequestCtx) {
					ctx.Response.SetBodyString("test body!")
				}
			},
			func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "http://localhost:8080/test", nil)
			},
			func(t *testing.T, res *http.Response) {
				body, _ := io.ReadAll(res.Body)
				assert.Equal(t, "test body!", string(body))
			},
		},
		{
			"Single headers are handled",
			func() fasthttp.RequestHandler {
				return func(ctx *fasthttp.RequestCtx) {
					ctx.Response.Header.SetContentType("application/json")
				}
			},
			func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "http://localhost:8080/test", nil)
			},
			func(t *testing.T, res *http.Response) {
				key := res.Header.Get("Content-Type")
				assert.Equal(t, "application/json", key)
			},
		},
		{
			"Duplicate headers are handled",
			func() fasthttp.RequestHandler {
				return func(ctx *fasthttp.RequestCtx) {
					ctx.Response.Header.Add("X-Transfer-Encoding", "chunked")
					ctx.Response.Header.Add("X-Transfer-Encoding", "compress")
					ctx.Response.Header.Add("X-Transfer-Encoding", "deflate")
					ctx.Response.Header.Add("X-Transfer-Encoding", "gzip")
				}
			},
			func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "http://localhost:8080/test", nil)
			},
			func(t *testing.T, res *http.Response) {
				encodings := res.TransferEncoding // TODO: How to set this property?
				if encodings == nil {
					encodings = res.Header["X-Transfer-Encoding"]
				}
				var chunked, compress, deflate, gzip bool
				for _, encoding := range encodings {
					switch encoding {
					case "chunked":
						chunked = true
					case "compress":
						compress = true
					case "deflate":
						deflate = true
					case "gzip":
						gzip = true
					}
				}
				assert.True(t, chunked, "expected chunked to be true but was false")
				assert.True(t, compress, "expected compress to be true but was false")
				assert.True(t, deflate, "expected deflate to be true but was false")
				assert.True(t, gzip, "expected gzip to be true but was false")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := tt.inputHandlerFactory()
			request := tt.inputRequestFactory()

			newNetHTTPHandler := NewNetHTTPHandlerFunc(handler)

			w := httptest.NewRecorder()
			newNetHTTPHandler.ServeHTTP(w, request)
			res := w.Result()
			defer res.Body.Close()
			tt.evaluate(t, res)
		})
	}
}
