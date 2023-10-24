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
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/test/bufconn"

	securityConsts "github.com/dapr/dapr/pkg/security/consts"
)

func TestAPITokenAuthMiddleware(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "👋")
	})

	const apiToken = "rosebud"

	assertPass := func(t *testing.T, w *httptest.ResponseRecorder) {
		t.Helper()

		res := w.Result()
		defer res.Body.Close()
		resBody, err := io.ReadAll(res.Body)
		require.NoError(t, err)

		assert.Equal(t, http.StatusOK, res.StatusCode)
		assert.Equal(t, "👋", string(resBody))
	}

	assertFail := func(t *testing.T, w *httptest.ResponseRecorder) {
		t.Helper()

		res := w.Result()
		defer res.Body.Close()
		resBody, err := io.ReadAll(res.Body)
		require.NoError(t, err)

		assert.Equal(t, http.StatusUnauthorized, res.StatusCode)
		assert.Equal(t, "invalid api token\n", string(resBody))
	}

	t.Run("no token required", func(t *testing.T) {
		mw := APITokenAuthMiddleware("")
		h := mw(handler)

		r := httptest.NewRequest(http.MethodGet, "/v1.0/foo", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assertPass(t, w)
	})

	t.Run("required token not provided", func(t *testing.T) {
		mw := APITokenAuthMiddleware(apiToken)
		h := mw(handler)

		r := httptest.NewRequest(http.MethodGet, "/v1.0/foo", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assertFail(t, w)
	})

	t.Run("with valid token", func(t *testing.T) {
		mw := APITokenAuthMiddleware(apiToken)
		h := mw(handler)

		r := httptest.NewRequest(http.MethodGet, "/v1.0/foo", nil)
		r.Header.Set(securityConsts.APITokenHeader, apiToken)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assertPass(t, w)
	})

	t.Run("healthz endpoints are always allowed", func(t *testing.T) {
		mw := APITokenAuthMiddleware(apiToken)
		h := mw(handler)

		t.Run("healthz", func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/v1.0/healthz", nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)

			assertPass(t, w)
		})

		t.Run("outbound healthz", func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/v1.0/healthz/outbound", nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)

			assertPass(t, w)
		})

		t.Run("querystring params are ignored", func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/v1.0/healthz?appid=myapp", nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)

			assertPass(t, w)
		})

		t.Run("ending slashes are trimmed", func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/v1.0/healthz/", nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)

			assertPass(t, w)
		})

		t.Run("non-get methods fail", func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPut, "/v1.0/healthz", nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)

			assertFail(t, w)
		})

		t.Run("must match exact path", func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/v1.0/invoke/myapp/method/healthz", nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)

			assertFail(t, w)
		})

		t.Run("must not match querystring", func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/v1.0/invoke/myapp/method/something?foo=/healthz", nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)

			assertFail(t, w)
		})
	})
}

// Below is a modified version of the code from https://github.com/go-chi/chi/blob/v5.0.8/middleware/strip_test.go
// Original code Copyright (c) 2015-present Peter Kieltyka (https://github.com/pkieltyka), Google Inc.
// Original code license: MIT: https://github.com/go-chi/chi/blob/v5.0.8/LICENSE

func TestStripSlashes(t *testing.T) {
	r := chi.NewRouter()

	// This middleware must be mounted at the top level of the router, not at the end-handler
	// because then it'll be too late and will end up in a 404
	r.Use(StripSlashesMiddleware)

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("nothing here"))
	})

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("root"))
	})

	r.Route("/accounts/{accountID}", func(r chi.Router) {
		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			accountID := chi.URLParam(r, "accountID")
			w.Write([]byte("account:" + accountID))
		})
	})

	// Do not remove the slash if it matches a route
	r.Get("/withslash/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello"))
	})

	client, teardown := testServer(r)
	defer teardown()

	if resp := testRequest(t, client, http.MethodGet, "/", nil); resp != "root" {
		t.Fatal(resp)
	}
	if resp := testRequest(t, client, http.MethodGet, "//", nil); resp != "root" {
		t.Fatal(resp)
	}
	if resp := testRequest(t, client, http.MethodGet, "/accounts/admin", nil); resp != "account:admin" {
		t.Fatal(resp)
	}
	if resp := testRequest(t, client, http.MethodGet, "/accounts/admin/", nil); resp != "account:admin" {
		t.Fatal(resp)
	}
	if resp := testRequest(t, client, http.MethodGet, "/accounts//", nil); resp != "account:" {
		t.Fatal(resp)
	}
	if resp := testRequest(t, client, http.MethodGet, "/nothing-here", nil); resp != "nothing here" {
		t.Fatal(resp)
	}
	if resp := testRequest(t, client, http.MethodGet, "/withslash/", nil); resp != "hello" {
		t.Fatal(resp)
	}
}

func TestStripSlashesInRoute(t *testing.T) {
	r := chi.NewRouter()

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("nothing here"))
	})

	r.Get("/hi", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hi"))
	})

	r.Route("/accounts/{accountID}", func(r chi.Router) {
		r.Use(StripSlashesMiddleware)
		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("accounts index"))
		})
		r.Get("/query", func(w http.ResponseWriter, r *http.Request) {
			accountID := chi.URLParam(r, "accountID")
			w.Write([]byte(accountID))
		})
	})

	client, teardown := testServer(r)
	defer teardown()

	if resp := testRequest(t, client, http.MethodGet, "/hi", nil); resp != "hi" {
		t.Fatal(resp)
	}
	if resp := testRequest(t, client, http.MethodGet, "/hi/", nil); resp != "nothing here" {
		t.Fatal(resp)
	}
	if resp := testRequest(t, client, http.MethodGet, "/accounts/admin", nil); resp != "accounts index" {
		t.Fatal(resp)
	}
	if resp := testRequest(t, client, http.MethodGet, "/accounts/admin/", nil); resp != "accounts index" {
		t.Fatal(resp)
	}
	if resp := testRequest(t, client, http.MethodGet, "/accounts/admin/query", nil); resp != "admin" {
		t.Fatal(resp)
	}
	if resp := testRequest(t, client, http.MethodGet, "/accounts/admin/query/", nil); resp != "admin" {
		t.Fatal(resp)
	}
}

// This tests a http.Handler that is not chi.Router
// In these cases, the routeContext is nil
func TestStripSlashesWithNilContext(t *testing.T) {
	r := http.NewServeMux()

	r.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("root"))
	})

	r.HandleFunc("/accounts", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("accounts"))
	})

	r.HandleFunc("/accounts/admin", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("admin"))
	})

	client, teardown := testServer(StripSlashesMiddleware(r))
	defer teardown()

	if resp := testRequest(t, client, http.MethodGet, "/", nil); resp != "root" {
		t.Fatal(resp)
	}
	if resp := testRequest(t, client, http.MethodGet, "//", nil); resp != "root" {
		t.Fatal(resp)
	}
	if resp := testRequest(t, client, http.MethodGet, "/accounts", nil); resp != "accounts" {
		t.Fatal(resp)
	}
	if resp := testRequest(t, client, http.MethodGet, "/accounts/", nil); resp != "accounts" {
		t.Fatal(resp)
	}
	if resp := testRequest(t, client, http.MethodGet, "/accounts/admin", nil); resp != "admin" {
		t.Fatal(resp)
	}
	if resp := testRequest(t, client, http.MethodGet, "/accounts/admin/", nil); resp != "admin" {
		t.Fatal(resp)
	}
}

func testServer(handler http.Handler) (client *http.Client, teardown func() error) {
	ln := bufconn.Listen(bufconnBufSize)
	teardown = ln.Close

	// Run in background and ignore errors - this will be closed when the listener is closed
	//nolint:gosec
	go http.Serve(ln, handler)

	client = &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return ln.DialContext(ctx)
			},
		},
	}

	return client, teardown
}

func testRequest(t *testing.T, client *http.Client, method, path string, body io.Reader) string {
	req, err := http.NewRequest(method, "http://test.com"+path, body)
	if err != nil {
		t.Fatal(err)
		return ""
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
		return ""
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
		return ""
	}

	return string(respBody)
}
