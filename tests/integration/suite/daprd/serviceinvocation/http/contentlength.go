/*
Copyright 2026 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package http

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(contentlength))
}

// contentlength tests that service invocation correctly forwards response
// bodies and that Content-Length matches the actual body.
type contentlength struct {
	daprd1 *procdaprd.Daprd
	daprd2 *procdaprd.Daprd
}

func (c *contentlength) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()

	handler.HandleFunc("/null", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("null"))
	})

	handler.HandleFunc("/empty", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler.HandleFunc("/json-object", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"key":"value"}`))
	})

	handler.HandleFunc("/plain-text", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("hello world"))
	})

	handler.HandleFunc("/large", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(strings.Repeat("x", 8192)))
	})

	handler.HandleFunc("/false", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("false"))
	})

	handler.HandleFunc("/zero", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("0"))
	})

	handler.HandleFunc("/empty-string", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`""`))
	})

	handler.HandleFunc("/stale-content-length", func(w http.ResponseWriter, _ *http.Request) {
		// App sends a WRONG Content-Length header on purpose.
		// Before the fix, daprd blindly forwarded this stale value
		// to the caller, causing a Content-Length / body mismatch.
		w.Header().Set("content-type", "application/json")
		w.Header().Set("Content-Length", "999")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`"ok"`))
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	c.daprd1 = procdaprd.New(t, procdaprd.WithAppPort(srv.Port()))
	c.daprd2 = procdaprd.New(t, procdaprd.WithAppPort(srv.Port()))

	return []framework.Option{
		framework.WithProcesses(srv, c.daprd1, c.daprd2),
	}
}

func (c *contentlength) Run(t *testing.T, ctx context.Context) {
	c.daprd1.WaitUntilRunning(t, ctx)
	c.daprd2.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	tests := []struct {
		method       string
		expectedBody string
	}{
		{method: "null", expectedBody: "null"},
		{method: "empty", expectedBody: ""},
		{method: "json-object", expectedBody: `{"key":"value"}`},
		{method: "plain-text", expectedBody: "hello world"},
		{method: "large", expectedBody: strings.Repeat("x", 8192)},
		{method: "false", expectedBody: "false"},
		{method: "zero", expectedBody: "0"},
		{method: "empty-string", expectedBody: `""`},
		// The critical regression test: app sends a wrong
		// Content-Length. Before the fix daprd forwarded it, causing
		// io.ReadAll to return "unexpected EOF" and a short body.
		{method: "stale-content-length", expectedBody: `"ok"`},
	}

	// Test local invocation (daprd1 -> daprd1's app).
	t.Run("local", func(t *testing.T) {
		for _, tc := range tests {
			t.Run(tc.method, func(t *testing.T) {
				url := fmt.Sprintf("http://%s/v1.0/invoke/%s/method/%s",
					c.daprd1.HTTPAddress(), c.daprd1.AppID(), tc.method)
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
				require.NoError(t, err)

				resp, err := httpClient.Do(req)
				require.NoError(t, err)

				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())

				require.Equal(t, http.StatusOK, resp.StatusCode)
				assert.Equal(t, tc.expectedBody, string(body))

				if cl := resp.Header.Get("Content-Length"); cl != "" {
					clInt, err := strconv.Atoi(cl)
					require.NoError(t, err)
					assert.Equal(t, len(body), clInt,
						"Content-Length header must match actual body length")
				}
			})
		}
	})

	// Test remote invocation (daprd2 -> daprd1's app via gRPC).
	t.Run("remote", func(t *testing.T) {
		for _, tc := range tests {
			t.Run(tc.method, func(t *testing.T) {
				url := fmt.Sprintf("http://%s/v1.0/invoke/%s/method/%s",
					c.daprd2.HTTPAddress(), c.daprd1.AppID(), tc.method)
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
				require.NoError(t, err)

				resp, err := httpClient.Do(req)
				require.NoError(t, err)

				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())

				require.Equal(t, http.StatusOK, resp.StatusCode)
				assert.Equal(t, tc.expectedBody, string(body))

				if cl := resp.Header.Get("Content-Length"); cl != "" {
					clInt, err := strconv.Atoi(cl)
					require.NoError(t, err)
					assert.Equal(t, len(body), clInt,
						"Content-Length header must match actual body length")
				}
			})
		}
	})
}
