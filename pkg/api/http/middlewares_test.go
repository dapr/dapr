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
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/test/bufconn"

	"github.com/dapr/dapr/pkg/api/http/endpoints"
	"github.com/dapr/dapr/pkg/config"
	securityConsts "github.com/dapr/dapr/pkg/security/consts"
	"github.com/dapr/kit/streams"
)

func TestMaxBodySizeMiddleware(t *testing.T) {
	const maxSize = 4

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err != nil {
			assert.ErrorIs(t, err, streams.ErrStreamTooLarge)
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	h := MaxBodySizeMiddleware(maxSize, nil)(handler)

	r := httptest.NewRequest(http.MethodPost, "/v1.0/state/store", strings.NewReader("large"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

func TestMaxBodySizeMiddlewareRouteContext(t *testing.T) {
	const maxSize = 4

	exempt := chi.NewRouter()
	exempt.Method(http.MethodPut, "/files/{name}", http.NotFoundHandler())

	for _, test := range []struct {
		name      string
		path      string
		routePath string
		context   bool
		limited   bool
	}{
		{
			name: "escaped path without route context",
			path: "/files/nested%2Ffile.bin",
		},
		{
			name:    "escaped path with empty route path",
			path:    "/files/nested%2Ffile.bin",
			context: true,
		},
		{
			name:      "cleaned route path takes precedence",
			path:      "/files//file.bin",
			routePath: "/files/file.bin",
			context:   true,
		},
		{
			name:      "non-exempt route path takes precedence",
			path:      "/files/file.bin",
			routePath: "/limited/file.bin",
			context:   true,
			limited:   true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPut, test.path, strings.NewReader("large"))
			var rctx *chi.Context
			if test.context {
				rctx = chi.NewRouteContext()
				rctx.RoutePath = test.routePath
				rctx.URLParams.Add("parent", "original")
				rctx.RoutePatterns = []string{"/parent/*"}
				r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
			}

			var (
				body                 []byte
				readErr              error
				observedRouteContext *chi.Context
			)
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, readErr = io.ReadAll(r.Body)
				observedRouteContext = chi.RouteContext(r.Context())
				w.WriteHeader(http.StatusNoContent)
			})

			w := httptest.NewRecorder()
			MaxBodySizeMiddleware(maxSize, exempt)(handler).ServeHTTP(w, r)
			require.Equal(t, http.StatusNoContent, w.Code)

			if test.limited {
				require.ErrorIs(t, readErr, streams.ErrStreamTooLarge)
				require.Len(t, body, maxSize)
			} else {
				require.NoError(t, readErr)
				require.Equal(t, "large", string(body))
			}

			if rctx == nil {
				require.Nil(t, observedRouteContext)
			} else {
				// Exemption matching must not add parameters or patterns
				// to the routing context used by downstream handlers.
				require.Same(t, rctx, observedRouteContext)
				require.Equal(t, test.routePath, rctx.RoutePath)
				require.Equal(t, []string{"parent"}, rctx.URLParams.Keys)
				require.Equal(t, []string{"original"}, rctx.URLParams.Values)
				require.Equal(t, []string{"/parent/*"}, rctx.RoutePatterns)
			}
		})
	}
}

func TestEndpointRequestBodySizeLimit(t *testing.T) {
	const maxSize = 4

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err != nil {
			assert.ErrorIs(t, err, streams.ErrStreamTooLarge)
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	srv := newServer()
	srv.config.MaxRequestBodySize = maxSize
	router := srv.getRouter()
	eps := []endpoints.Endpoint{
		{
			Methods: []string{http.MethodPost},
			Route:   "state/store",
			Version: apiVersionV1,
			Handler: handler,
		},
		{
			Methods: []string{http.MethodPut, http.MethodPost},
			Route:   "binarystore/{name}/{fileName}",
			Version: apiVersionV1alpha1,
			Handler: handler,
			Settings: endpoints.EndpointSettings{
				SkipRequestBodySizeLimit: true,
			},
		},
		{
			Methods: []string{http.MethodPatch},
			Route:   "binarystore/{name}/{fileName}",
			Version: apiVersionV1alpha1,
			Handler: handler,
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "wild/*",
			Version: apiVersionV1,
			Handler: handler,
			Settings: endpoints.EndpointSettings{
				SkipRequestBodySizeLimit: true,
			},
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "regex/{id:[a-z]+}",
			Version: apiVersionV1,
			Handler: handler,
			Settings: endpoints.EndpointSettings{
				SkipRequestBodySizeLimit: true,
			},
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "regex/{id:[0-9]+}",
			Version: apiVersionV1,
			Handler: handler,
		},
		{
			Route:   "any/{name}",
			Version: apiVersionV1,
			Handler: handler,
			Settings: endpoints.EndpointSettings{
				SkipRequestBodySizeLimit: true,
			},
		},
	}
	srv.useMaxBodySize(router, eps)
	srv.setupRoutes(router, eps)
	router.NotFound(handler)

	tests := []struct {
		name       string
		method     string
		path       string
		statusCode int
	}{
		{
			name:       "regular endpoint is limited",
			method:     http.MethodPost,
			path:       "/v1.0/state/store",
			statusCode: http.StatusRequestEntityTooLarge,
		},
		{
			name:       "streaming endpoint is unlimited",
			method:     http.MethodPut,
			path:       "/v1.0-alpha1/binarystore/store/file.bin",
			statusCode: http.StatusNoContent,
		},
		{
			name:       "POST streaming endpoint is unlimited",
			method:     http.MethodPost,
			path:       "/v1.0-alpha1/binarystore/store/file.bin",
			statusCode: http.StatusNoContent,
		},
		{
			name:       "escaped file name is unlimited",
			method:     http.MethodPut,
			path:       "/v1.0-alpha1/binarystore/store/nested%2Ffile.bin",
			statusCode: http.StatusNoContent,
		},
		{
			name:       "cleaned streaming endpoint is unlimited",
			method:     http.MethodPut,
			path:       "/v1.0-alpha1/binarystore//store/file.bin",
			statusCode: http.StatusNoContent,
		},
		{
			name:       "same route with non-exempt method is limited",
			method:     http.MethodPatch,
			path:       "/v1.0-alpha1/binarystore/store/file.bin",
			statusCode: http.StatusRequestEntityTooLarge,
		},
		{
			name:       "wildcard streaming endpoint is unlimited",
			method:     http.MethodPost,
			path:       "/v1.0/wild/a/b/c",
			statusCode: http.StatusNoContent,
		},
		{
			name:       "regex param matching streaming endpoint is unlimited",
			method:     http.MethodPost,
			path:       "/v1.0/regex/abc",
			statusCode: http.StatusNoContent,
		},
		{
			name:       "regex param matching regular endpoint is limited",
			method:     http.MethodPost,
			path:       "/v1.0/regex/123",
			statusCode: http.StatusRequestEntityTooLarge,
		},
		{
			name:       "regex mismatch is limited",
			method:     http.MethodPost,
			path:       "/v1.0/regex/abc123",
			statusCode: http.StatusRequestEntityTooLarge,
		},
		{
			name:       "all-method endpoint allows POST",
			method:     http.MethodPost,
			path:       "/v1.0/any/file.bin",
			statusCode: http.StatusNoContent,
		},
		{
			name:       "all-method endpoint allows DELETE",
			method:     http.MethodDelete,
			path:       "/v1.0/any/file.bin",
			statusCode: http.StatusNoContent,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(test.method, test.path, strings.NewReader("large"))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			assert.Equal(t, test.statusCode, w.Code)
		})
	}
}

// The body limit must be applied before any other middleware so the
// middleware pipeline never observes an unbounded body, while endpoints that
// opt out via SkipRequestBodySizeLimit stream unbounded bodies through it.
func TestMaxBodySizeMiddlewareRunsBeforePipeline(t *testing.T) {
	const maxSize = 4

	type observation struct {
		path      string
		bytesRead int
		err       error
	}
	var observations []observation
	probe := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			observations = append(observations, observation{r.URL.EscapedPath(), len(body), err})
			r.Body = io.NopCloser(bytes.NewReader(body))
			next.ServeHTTP(w, r)
		})
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusNoContent)
	})

	srv := newServer()
	srv.config.MaxRequestBodySize = maxSize
	router := srv.getRouter()
	eps := []endpoints.Endpoint{
		{
			Methods: []string{http.MethodPost},
			Route:   "state/store",
			Version: apiVersionV1,
			Handler: handler,
		},
		{
			Methods: []string{http.MethodPut},
			Route:   "binarystore/{name}/{fileName}",
			Version: apiVersionV1alpha1,
			Handler: handler,
			Settings: endpoints.EndpointSettings{
				SkipRequestBodySizeLimit: true,
			},
		},
	}
	srv.useMaxBodySize(router, eps)
	// Registered after the limiter, like the components middleware pipeline.
	router.Use(probe)
	srv.setupRoutes(router, eps)

	r := httptest.NewRequest(http.MethodPost, "/v1.0/state/store", strings.NewReader("large"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	require.Equal(t, http.StatusNoContent, w.Code)

	r = httptest.NewRequest(http.MethodPut, "/v1.0-alpha1/binarystore/store/file.bin", strings.NewReader("large"))
	w = httptest.NewRecorder()
	router.ServeHTTP(w, r)
	require.Equal(t, http.StatusNoContent, w.Code)

	require.Len(t, observations, 2)

	assert.Equal(t, "/v1.0/state/store", observations[0].path)
	assert.Equal(t, maxSize, observations[0].bytesRead)
	require.ErrorIs(t, observations[0].err, streams.ErrStreamTooLarge)

	assert.Equal(t, "/v1.0-alpha1/binarystore/store/file.bin", observations[1].path)
	assert.Equal(t, len("large"), observations[1].bytesRead)
	assert.NoError(t, observations[1].err)
}

// An endpoint that opts out of the body size limit must only be exempt when
// it is actually registered: a denied or non-allowlisted endpoint must not
// become a way to send an unbounded body through the middleware pipeline.
func TestMaxBodySizeExemptionRespectsAPIAccessRules(t *testing.T) {
	const maxSize = 4

	newEndpoints := func(handler http.HandlerFunc) []endpoints.Endpoint {
		return []endpoints.Endpoint{
			{
				Methods: []string{http.MethodPost},
				Route:   "state/store",
				Version: apiVersionV1,
				Group: &endpoints.EndpointGroup{
					Name:    endpoints.EndpointGroupState,
					Version: endpoints.EndpointGroupVersion1,
				},
				Handler: handler,
			},
			{
				Methods: []string{http.MethodPut},
				Route:   "binarystore/{name}/{fileName}",
				Version: apiVersionV1alpha1,
				Group:   endpointGroupBinaryStoreV1Alpha1,
				Handler: handler,
				Settings: endpoints.EndpointSettings{
					SkipRequestBodySizeLimit: true,
				},
			},
		}
	}

	tests := []struct {
		name    string
		apiSpec config.APISpec
		// Whether the binarystore endpoint is expected to be registered.
		registered bool
	}{
		{
			name:       "no rules: exempt",
			registered: true,
		},
		{
			name: "denied: limited",
			apiSpec: config.APISpec{
				Denied: config.APIAccessRules{
					{Name: "binarystore", Version: "v1alpha1", Protocol: config.APIAccessRuleProtocolHTTP},
				},
			},
		},
		{
			name: "not in allowlist: limited",
			apiSpec: config.APISpec{
				Allowed: config.APIAccessRules{
					{Name: "state", Version: "v1", Protocol: config.APIAccessRuleProtocolHTTP},
				},
			},
		},
		{
			name: "in allowlist: exempt",
			apiSpec: config.APISpec{
				Allowed: config.APIAccessRules{
					{Name: "binarystore", Version: "v1alpha1", Protocol: config.APIAccessRuleProtocolHTTP},
				},
			},
			registered: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var (
				probeBytes int
				probeErr   error
			)
			probe := func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					probeBytes, probeErr = len(body), err
					r.Body = io.NopCloser(bytes.NewReader(body))
					next.ServeHTTP(w, r)
				})
			}
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			})

			srv := newServer()
			srv.config.MaxRequestBodySize = maxSize
			srv.apiSpec = test.apiSpec
			router := srv.getRouter()
			eps := srv.allowedEndpoints(newEndpoints(handler))
			srv.useMaxBodySize(router, eps)
			router.Use(probe)
			srv.setupRoutes(router, eps)

			r := httptest.NewRequest(http.MethodPut, "/v1.0-alpha1/binarystore/store/file.bin", strings.NewReader("large"))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)

			if test.registered {
				assert.Equal(t, http.StatusNoContent, w.Code)
				assert.Equal(t, len("large"), probeBytes)
				require.NoError(t, probeErr)
			} else {
				assert.NotEqual(t, http.StatusNoContent, w.Code)
				assert.Equal(t, maxSize, probeBytes)
				require.ErrorIs(t, probeErr, streams.ErrStreamTooLarge)
			}
		})
	}
}

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

		t.Run("percent-encoded paths fail", func(t *testing.T) {
			for _, p := range []string{"/v1.0/healt%68z", "/v1.0%2Fhealthz"} {
				r := httptest.NewRequest(http.MethodGet, p, nil)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)

				assertFail(t, w)
			}
		})

		t.Run("percent-encoded paths fail behind router", func(t *testing.T) {
			router := chi.NewRouter()
			router.Use(CleanPathMiddleware, StripSlashesMiddleware, mw)
			router.Get("/v1.0/healthz", handler)
			router.NotFound(handler)

			for _, p := range []string{"/v1.0/healt%68z", "/v1.0%2Fhealthz"} {
				r := httptest.NewRequest(http.MethodGet, p, nil)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, r)

				assertFail(t, w)
			}

			r := httptest.NewRequest(http.MethodGet, "/v1.0/healthz", nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)

			assertPass(t, w)
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
