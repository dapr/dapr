/*
Copyright 2026 The Dapr Authors
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
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(hopbyhop))
}

type hopbyhop struct {
	daprd1 *procdaprd.Daprd
	daprd2 *procdaprd.Daprd

	daprdEndpoint *procdaprd.Daprd
}

var hopByHopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Connection",
	"Transfer-Encoding",
	"Upgrade",
	"HTTP2-Settings",
	"TE",
	"Trailer",
	"Proxy-Authorization",
	"Proxy-Authenticate",
}

func (h *hopbyhop) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()

	handler.HandleFunc("/echo-request-headers", func(w http.ResponseWriter, r *http.Request) {
		var sb strings.Builder
		for k, vals := range r.Header {
			for _, v := range vals {
				sb.WriteString(k + ": " + v + "\n")
			}
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(sb.String()))
	})

	handler.HandleFunc("/respond-with-hop-by-hop", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "upgrade")
		w.Header().Set("Upgrade", "h2c")
		w.Header().Set("Keep-Alive", "timeout=5")
		w.Header().Set("Transfer-Encoding", "chunked")
		w.Header().Set("Proxy-Connection", "keep-alive")
		w.Header().Set("HTTP2-Settings", "AAMAAABkAAQAAP__")
		w.Header().Set("TE", "trailers")
		w.Header().Set("Trailer", "X-Checksum")
		w.Header().Set("Proxy-Authorization", "Basic dGVzdDp0ZXN0")
		w.Header().Set("Proxy-Authenticate", "Basic realm=test")

		w.Header().Set("X-Custom-Response", "should-be-forwarded")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	handler.HandleFunc("/passthrough", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom-Response", "present")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))

	h.daprd1 = procdaprd.New(t, procdaprd.WithAppPort(srv.Port()))
	h.daprd2 = procdaprd.New(t, procdaprd.WithAppPort(srv.Port()))

	h.daprdEndpoint = procdaprd.New(t, procdaprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: HTTPEndpoint
metadata:
  name: upstream
spec:
  version: v1alpha1
  baseUrl: http://localhost:%d
`, srv.Port())))

	return []framework.Option{
		framework.WithProcesses(srv, h.daprd1, h.daprd2, h.daprdEndpoint),
	}
}

func (h *hopbyhop) Run(t *testing.T, ctx context.Context) {
	h.daprd1.WaitUntilRunning(t, ctx)
	h.daprd2.WaitUntilRunning(t, ctx)
	h.daprdEndpoint.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	doReq := func(t require.TestingT, method, url string, headers map[string]string) (int, string, http.Header) {
		if h, ok := t.(interface{ Helper() }); ok {
			h.Helper()
		}
		req, err := http.NewRequestWithContext(ctx, method, url, nil)
		require.NoError(t, err)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return resp.StatusCode, string(body), resp.Header
	}

	t.Run("request hop-by-hop stripped on local invocation", func(t *testing.T) {
		// Use non-triggering values: "Connection: Upgrade" + "Upgrade: h2c" would
		// cause Go's HTTP server to perform an actual h2c protocol switch (101
		// Switching Protocols), so we avoid that combination.
		reqHeaders := map[string]string{
			"Connection":          "keep-alive",
			"Upgrade":             "websocket",
			"HTTP2-Settings":      "AAMAAABkAAQAAP__",
			"Keep-Alive":          "timeout=5, max=100",
			"TE":                  "trailers",
			"Trailer":             "X-Checksum",
			"Proxy-Authorization": "Basic dGVzdDp0ZXN0",
			"Proxy-Connection":    "keep-alive",

			"X-Custom-Header": "should-survive",
			"Accept":          "application/json",
		}

		url := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/passthrough",
			h.daprd1.HTTPPort(), h.daprd1.AppID())
		status, body, respHeader := doReq(t, http.MethodGet, url, reqHeaders)

		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, "ok", body)

		assert.Equal(t, "present", respHeader.Get("X-Custom-Response"))
	})

	t.Run("request hop-by-hop stripped on remote invocation", func(t *testing.T) {
		reqHeaders := map[string]string{
			"Connection":     "keep-alive",
			"Upgrade":        "websocket",
			"HTTP2-Settings": "AAMAAABkAAQAAP__",
			"Keep-Alive":     "timeout=5",
			"X-Custom":       "should-survive",
		}

		url := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/passthrough",
			h.daprd2.HTTPPort(), h.daprd1.AppID())
		status, body, respHeader := doReq(t, http.MethodGet, url, reqHeaders)

		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, "ok", body)
		assert.Equal(t, "present", respHeader.Get("X-Custom-Response"))
	})

	t.Run("request hop-by-hop stripped on HTTPEndpoint invocation", func(t *testing.T) {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			url := fmt.Sprintf("http://localhost:%d/v1.0/invoke/upstream/method/passthrough",
				h.daprdEndpoint.HTTPPort())
			status, body, _ := doReq(c, http.MethodGet, url, map[string]string{
				"Connection":          "keep-alive",
				"Upgrade":             "websocket",
				"HTTP2-Settings":      "AAMAAABkAAQAAP__",
				"Keep-Alive":          "timeout=5",
				"TE":                  "trailers",
				"Trailer":             "X-Checksum",
				"Proxy-Authorization": "Basic dGVzdDp0ZXN0",
				"Proxy-Connection":    "keep-alive",
				"X-Custom-Header":     "should-survive",
			})
			assert.Equal(c, http.StatusOK, status)
			assert.Equal(c, "ok", body)
		}, time.Second*20, time.Millisecond*200)
	})

	t.Run("response hop-by-hop stripped on local invocation", func(t *testing.T) {
		url := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/respond-with-hop-by-hop",
			h.daprd1.HTTPPort(), h.daprd1.AppID())
		status, body, respHeader := doReq(t, http.MethodGet, url, nil)

		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, "ok", body)

		for _, hdr := range hopByHopHeaders {
			assert.Empty(t, respHeader.Get(hdr),
				"hop-by-hop response header %q should be stripped", hdr)
		}

		assert.Equal(t, "should-be-forwarded", respHeader.Get("X-Custom-Response"))
	})

	t.Run("response hop-by-hop stripped on remote invocation", func(t *testing.T) {
		url := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/respond-with-hop-by-hop",
			h.daprd2.HTTPPort(), h.daprd1.AppID())
		status, body, respHeader := doReq(t, http.MethodGet, url, nil)

		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, "ok", body)

		for _, hdr := range hopByHopHeaders {
			assert.Empty(t, respHeader.Get(hdr),
				"hop-by-hop response header %q should be stripped", hdr)
		}

		assert.Equal(t, "should-be-forwarded", respHeader.Get("X-Custom-Response"))
	})

	t.Run("response hop-by-hop stripped on HTTPEndpoint invocation", func(t *testing.T) {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			url := fmt.Sprintf("http://localhost:%d/v1.0/invoke/upstream/method/respond-with-hop-by-hop",
				h.daprdEndpoint.HTTPPort())
			status, body, respHeader := doReq(c, http.MethodGet, url, nil)
			assert.Equal(c, http.StatusOK, status)
			assert.Equal(c, "ok", body)

			for _, hdr := range hopByHopHeaders {
				assert.Empty(c, respHeader.Get(hdr),
					"hop-by-hop response header %q should be stripped", hdr)
			}

			assert.Equal(c, "should-be-forwarded", respHeader.Get("X-Custom-Response"))
		}, time.Second*20, time.Millisecond*200)
	})

	t.Run("individual request hop-by-hop headers stripped", func(t *testing.T) {
		for _, hdr := range hopByHopHeaders {
			t.Run(hdr, func(t *testing.T) {
				url := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/echo-request-headers",
					h.daprd1.HTTPPort(), h.daprd1.AppID())
				status, body, _ := doReq(t, http.MethodGet, url, map[string]string{
					hdr:            "test-value",
					"X-End-To-End": "should-survive",
				})

				assert.Equal(t, http.StatusOK, status)
				// Use canonical header key since Go's HTTP server
				// canonicalizes header names (e.g. HTTP2-Settings -> Http2-Settings).
				canonicalHdr := http.CanonicalHeaderKey(hdr)
				assert.NotContains(t, body, canonicalHdr+": test-value",
					"hop-by-hop request header %q should not be forwarded to app", hdr)
			})
		}
	})

	t.Run("individual response hop-by-hop headers stripped", func(t *testing.T) {
		for _, hdr := range hopByHopHeaders {
			url := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/respond-with-hop-by-hop",
				h.daprd1.HTTPPort(), h.daprd1.AppID())
			_, _, respHeader := doReq(t, http.MethodGet, url, nil)
			assert.Empty(t, respHeader.Get(hdr),
				"hop-by-hop response header %q should be stripped", hdr)
		}
	})

	t.Run("end-to-end headers survive with hop-by-hop stripped", func(t *testing.T) {
		endToEndHeaders := map[string]string{
			"Accept":          "application/json",
			"Authorization":   "Bearer token123",
			"X-Request-Id":    "req-001",
			"X-Custom-Header": "custom-value",
		}
		hopHeaders := map[string]string{
			"Connection":     "keep-alive",
			"Upgrade":        "websocket",
			"HTTP2-Settings": "AAMAAABkAAQAAP__",
			"Keep-Alive":     "timeout=5",
		}

		allHeaders := make(map[string]string)
		maps.Copy(allHeaders, endToEndHeaders)
		maps.Copy(allHeaders, hopHeaders)

		url := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/echo-request-headers",
			h.daprd1.HTTPPort(), h.daprd1.AppID())
		status, body, _ := doReq(t, http.MethodGet, url, allHeaders)

		assert.Equal(t, http.StatusOK, status)

		for hdr, val := range endToEndHeaders {
			assert.Contains(t, body, hdr+": "+val,
				"end-to-end header %q should be forwarded to app", hdr)
		}

		for hdr := range hopHeaders {
			canonicalHdr := http.CanonicalHeaderKey(hdr)
			assert.NotContains(t, body, canonicalHdr+": ",
				"hop-by-hop header %q should not be forwarded to app", hdr)
		}
	})

	t.Run("hop-by-hop stripped across HTTP methods", func(t *testing.T) {
		methods := []string{
			http.MethodGet,
			http.MethodPost,
			http.MethodPut,
			http.MethodDelete,
			http.MethodPatch,
		}

		hopHeaders := map[string]string{
			"Connection":     "keep-alive",
			"Upgrade":        "websocket",
			"HTTP2-Settings": "AAMAAABkAAQAAP__",
		}

		for _, method := range methods {
			t.Run(method, func(t *testing.T) {
				url := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/echo-request-headers",
					h.daprd1.HTTPPort(), h.daprd1.AppID())
				status, body, _ := doReq(t, method, url, hopHeaders)

				assert.Equal(t, http.StatusOK, status)

				for hdr := range hopHeaders {
					canonicalHdr := http.CanonicalHeaderKey(hdr)
					assert.NotContains(t, body, canonicalHdr+": ",
						"hop-by-hop header %q should not be forwarded for %s", hdr, method)
				}
			})
		}
	})

	t.Run("hop-by-hop stripped with dapr-app-id header invocation", func(t *testing.T) {
		url := fmt.Sprintf("http://localhost:%d/passthrough", h.daprd1.HTTPPort())
		status, body, respHeader := doReq(t, http.MethodGet, url, map[string]string{
			"dapr-app-id":    h.daprd2.AppID(),
			"Connection":     "keep-alive",
			"Upgrade":        "websocket",
			"HTTP2-Settings": "AAMAAABkAAQAAP__",
			"X-Custom":       "should-survive",
		})

		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, "ok", body)
		assert.Equal(t, "present", respHeader.Get("X-Custom-Response"))
	})

	t.Run("hop-by-hop stripped with direct URL invocation", func(t *testing.T) {
		url := fmt.Sprintf("http://localhost:%d/v1.0/invoke/http://localhost:%d/method/passthrough",
			h.daprd1.HTTPPort(), h.daprd1.AppPort(t))
		status, body, _ := doReq(t, http.MethodGet, url, map[string]string{
			"Connection":     "keep-alive",
			"Upgrade":        "websocket",
			"HTTP2-Settings": "AAMAAABkAAQAAP__",
		})

		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, "ok", body)
	})
}
