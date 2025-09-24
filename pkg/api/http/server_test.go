/*
Copyright 2023 The Dapr Authors
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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/phayes/freeport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/api/http/endpoints"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/cors"
	dapr_testing "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/logger"
)

func newServer() server {
	return server{
		config: ServerConfig{},
	}
}

func TestCorsHandler(t *testing.T) {
	hf := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	t.Run("with default cors, middleware allow all", func(t *testing.T) {
		srv := newServer()
		srv.config.AllowedOrigins = cors.DefaultAllowedOrigins

		h := chi.NewRouter()
		srv.useCors(h)
		h.Get("/", hf)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodOptions, "/", nil)
		r.Header.Set("Origin", "*")
		r.Header.Set("Access-Control-Request-Method", "GET")
		h.ServeHTTP(w, r)

		assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("with default cors, api token auth not required", func(t *testing.T) {
		srv := newServer()
		srv.config.AllowedOrigins = cors.DefaultAllowedOrigins

		h := chi.NewRouter()
		srv.useCors(h)
		// register API authentication middleware after CORS middleware
		h.Use(APITokenAuthMiddleware("test"))
		h.Get("/", hf)

		t.Run("OPTIONS request, without api token", func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodOptions, "/", nil)
			r.Header.Set("Origin", "*")
			r.Header.Set("Access-Control-Request-Method", "GET")
			// OPTIONS request does not usually include the API token
			h.ServeHTTP(w, r)
			assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
			assert.Equal(t, http.StatusOK, w.Result().StatusCode)
		})

		t.Run("OPTIONS request, with api token", func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodOptions, "/", nil)
			r.Header.Set("Origin", "*")
			r.Header.Set("Access-Control-Request-Method", "GET")
			r.Header.Set("dapr-api-token", "test")
			h.ServeHTTP(w, r)
			assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
			assert.Equal(t, http.StatusOK, w.Result().StatusCode)
		})

		t.Run("GET request, without api token", func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			h.ServeHTTP(w, r)
			assert.Equal(t, http.StatusUnauthorized, w.Result().StatusCode)
		})

		t.Run("GET request, with api token", func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.Header.Set("dapr-api-token", "test")
			h.ServeHTTP(w, r)
			assert.Equal(t, http.StatusOK, w.Result().StatusCode)
		})
	})

	t.Run("with custom cors, middleware enabled", func(t *testing.T) {
		srv := newServer()
		srv.config.AllowedOrigins = "http://test.com"

		h := chi.NewRouter()
		srv.useCors(h)
		h.Get("/", hf)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodOptions, "/", nil)
		r.Header.Set("Origin", "http://test.com")
		r.Header.Set("Access-Control-Request-Method", "GET")
		h.ServeHTTP(w, r)

		assert.NotEmpty(t, w.Header().Get("Access-Control-Allow-Origin"))
	})
}

func TestUnescapeRequestParametersHandler(t *testing.T) {
	mh := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var param string
		chiCtx := chi.RouteContext(r.Context())
		if chiCtx != nil {
			param = chiCtx.URLParam("testparam")
		}
		w.Write([]byte(param))
	})

	// Create a context that has a URL parameter, which will allow us to determine if the UnescapeRequestParameters middleware was invoked
	newCtx := func() context.Context {
		chiCtx := chi.NewRouteContext()
		chiCtx.URLParams.Add("testparam", "foo%20bar")
		return context.WithValue(t.Context(), chi.RouteCtxKey, chiCtx)
	}

	t.Run("unescapeRequestParametersHandler is added as middleware if the endpoint includes Parameters in its path", func(t *testing.T) {
		endpoints := []endpoints.Endpoint{
			{
				Methods: []string{http.MethodGet},
				Route:   "state/{storeName}/{key}",
				Version: apiVersionV1,
				Handler: mh,
			},
			{
				Methods: []string{http.MethodGet},
				Route:   "secrets/{secretStoreName}/{key}",
				Version: apiVersionV1,
				Handler: mh,
			},
			{
				Methods: []string{http.MethodPost, http.MethodPut},
				Route:   "publish/{pubsubname}/{topic:*}",
				Version: apiVersionV1,
				Handler: mh,
			},
			{
				Methods: []string{http.MethodGet, http.MethodPost, http.MethodDelete, http.MethodPut},
				Route:   "actors/{actorType}/{actorId}/method/{method}",
				Version: apiVersionV1,
				Handler: mh,
			},
		}

		srv := newServer()
		router := chi.NewRouter()
		srv.setupRoutes(router, endpoints)

		for _, e := range endpoints {
			path := fmt.Sprintf("/%s/%s", e.Version, e.Route)
			for _, m := range e.Methods {
				req := httptest.NewRequest(m, path, nil)
				req = req.WithContext(newCtx())
				rw := httptest.NewRecorder()
				router.ServeHTTP(rw, req)

				res := rw.Result()
				defer res.Body.Close()
				bodyData, err := io.ReadAll(res.Body)
				require.NoError(t, err)
				assert.Equal(t, "foo bar", string(bodyData))
			}
		}
	})

	t.Run("unescapeRequestParameterHandler is not added as middleware if the endpoint does not include Parameters in its path", func(t *testing.T) {
		endpoints := []endpoints.Endpoint{
			{
				Methods: []string{http.MethodGet},
				Route:   "metadata",
				Version: apiVersionV1,
				Handler: mh,
			},
			{
				Methods: []string{http.MethodGet},
				Route:   "healthz",
				Version: apiVersionV1,
				Handler: mh,
			},
		}

		srv := newServer()
		router := chi.NewRouter()
		srv.setupRoutes(router, endpoints)

		for _, e := range endpoints {
			path := fmt.Sprintf("/%s/%s", e.Version, e.Route)
			for _, m := range e.Methods {
				req := httptest.NewRequest(m, path, nil)
				req = req.WithContext(newCtx())
				rw := httptest.NewRecorder()
				router.ServeHTTP(rw, req)

				res := rw.Result()
				defer res.Body.Close()
				bodyData, err := io.ReadAll(res.Body)
				require.NoError(t, err)
				assert.Equal(t, "foo%20bar", string(bodyData))
			}
		}
	})

	t.Run("Unescape valid Parameters", func(t *testing.T) {
		parameters := []map[string]string{
			{
				"parameterName":          "stateStore",
				"parameterValue":         "unknown%20state%20store",
				"expectedParameterValue": "unknown state store",
			},
			{
				"parameterName":          "stateStore",
				"parameterValue":         "stateStore%2F1",
				"expectedParameterValue": "stateStore/1",
			},
			{
				"parameterName":          "stateStore",
				"parameterValue":         "stateStore%2Fprod",
				"expectedParameterValue": "stateStore/prod",
			},
			{
				"parameterName":          "secretStore",
				"parameterValue":         "unknown%20secret%20store",
				"expectedParameterValue": "unknown secret store",
			},
			{
				"parameterName":          "actorType",
				"parameterValue":         "my%20actor",
				"expectedParameterValue": "my actor",
			},
		}
		srv := newServer()

		for _, parameter := range parameters {
			chiCtx := chi.NewRouteContext()
			chiCtx.URLParams.Add(parameter["parameterName"], parameter["parameterValue"])
			err := srv.unespaceRequestParametersInContext(chiCtx)
			require.NoError(t, err)
			assert.Equal(t, parameter["expectedParameterValue"], chiCtx.URLParam(parameter["parameterName"]))
		}
	})

	t.Run("Unescape invalid Parameters", func(t *testing.T) {
		parameters := []map[string]string{
			{
				"parameterName":  "stateStore",
				"parameterValue": "unknown%2state%20store",
			},
			{
				"parameterName":  "stateStore",
				"parameterValue": "stateStore%2prod",
			},
			{
				"parameterName":  "secretStore",
				"parameterValue": "unknown%20secret%2store",
			},
		}

		srv := newServer()

		for _, parameter := range parameters {
			chiCtx := chi.NewRouteContext()
			chiCtx.URLParams.Add(parameter["parameterName"], parameter["parameterValue"])
			err := srv.unespaceRequestParametersInContext(chiCtx)
			require.Error(t, err)
			require.ErrorContains(t, err, "failed to unescape request parameter")
		}
	})
}

func TestAPILogging(t *testing.T) {
	// Replace the logger with a custom one for testing
	prev := infoLog
	logDest := &bytes.Buffer{}
	infoLog = logger.NewLogger("test-api-logging")
	infoLog.EnableJSONOutput(true)
	infoLog.SetOutput(io.MultiWriter(logDest, os.Stderr))
	defer func() {
		infoLog = prev
	}()

	body := []byte("👋")

	endpoints := []endpoints.Endpoint{
		{
			Methods: []string{http.MethodGet, http.MethodPost},
			Route:   "state/{storeName}/{key}",
			Version: apiVersionV1,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write(body)
			}),
			Settings: endpoints.EndpointSettings{
				Name: "GetState",
			},
		},
	}
	srv := newServer()
	srv.config.EnableAPILogging = true

	router := chi.NewRouter()
	srv.useContextSetup(router)
	srv.useAPILogging(router)
	srv.setupRoutes(router, endpoints)

	dec := json.NewDecoder(logDest)

	runTest := func(userAgent string, obfuscateURL bool) func(t *testing.T) {
		return func(t *testing.T) {
			srv.config.APILoggingObfuscateURLs = obfuscateURL

			for _, e := range endpoints {
				path := fmt.Sprintf("/%s/%s", e.Version, "state/mystate/mykey")
				for _, m := range e.Methods {
					req := httptest.NewRequest(m, path, nil)
					req.Header.Set("user-agent", userAgent)
					rw := httptest.NewRecorder()

					router.ServeHTTP(rw, req)

					resp := rw.Result()
					defer resp.Body.Close()

					respBody, err := io.ReadAll(resp.Body)
					require.NoError(t, err)
					assert.Equal(t, body, respBody)

					logData := map[string]any{}
					err = dec.Decode(&logData)
					require.NoError(t, err)

					assert.Equal(t, "test-api-logging", logData["scope"])
					assert.Equal(t, "HTTP API Called", logData["msg"])

					if obfuscateURL {
						assert.Equal(t, e.Settings.Name, logData["method"])
					} else {
						assert.Equal(t, m+" "+path, logData["method"])
					}

					timeStr, ok := logData["time"].(string)
					assert.True(t, ok)
					tt, err := time.Parse(time.RFC3339Nano, timeStr)
					require.NoError(t, err)
					assert.InDelta(t, time.Now().Unix(), tt.Unix(), 120)

					// In our test the duration better be no more than 10ms!
					dur, ok := logData["duration"].(float64)
					assert.True(t, ok)
					assert.Less(t, dur, 10.0)

					assert.InDelta(t, float64(len(body)), logData["size"], 0)
					assert.InDelta(t, float64(http.StatusOK), logData["code"], 0)

					if userAgent != "" {
						assert.Equal(t, userAgent, logData["useragent"])
					} else {
						_, found := logData["useragent"]
						assert.False(t, found)
					}
				}
			}
		}
	}

	// Test user agent inclusion
	t.Run("without user agent", runTest("", false))
	t.Run("with user agent", runTest("daprtest/1", false))

	// Test obfuscate URLs
	t.Run("obfuscate URL", runTest("daprtest/1", true))
}

func TestAPILoggingOmitHealthChecks(t *testing.T) {
	// Replace the logger with a custom one for testing
	prev := infoLog
	logDest := &bytes.Buffer{}
	infoLog = logger.NewLogger("test-api-logging")
	infoLog.EnableJSONOutput(true)
	infoLog.SetOutput(io.MultiWriter(logDest, os.Stderr))
	defer func() {
		infoLog = prev
	}()

	body := []byte("👋")

	endpoints := []endpoints.Endpoint{
		{
			Methods: []string{http.MethodGet},
			Route:   "log",
			Version: apiVersionV1,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write(body)
			}),
			Settings: endpoints.EndpointSettings{
				IsHealthCheck: false,
			},
		},
		{
			Methods: []string{http.MethodGet},
			Route:   "nolog",
			Version: apiVersionV1,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write(body)
			}),
			Settings: endpoints.EndpointSettings{
				IsHealthCheck: true,
			},
		},
	}
	srv := newServer()
	srv.config.EnableAPILogging = true
	srv.config.APILogHealthChecks = false

	router := chi.NewRouter()
	srv.useContextSetup(router)
	srv.useAPILogging(router)
	srv.setupRoutes(router, endpoints)

	dec := json.NewDecoder(logDest)

	for _, e := range endpoints {
		path := fmt.Sprintf("/%s/%s", e.Version, e.Route)

		req := httptest.NewRequest(http.MethodGet, path, nil)
		rw := httptest.NewRecorder()

		router.ServeHTTP(rw, req)

		resp := rw.Result()
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, body, respBody)

		if e.Route == "log" {
			logData := map[string]any{}
			err := dec.Decode(&logData)
			require.NoError(t, err)

			assert.Equal(t, "test-api-logging", logData["scope"])
			assert.Equal(t, "HTTP API Called", logData["msg"])
			assert.Equal(t, "GET "+path, logData["method"])
		} else {
			require.Empty(t, logDest.Bytes())
		}
	}
}

func TestClose(t *testing.T) {
	t.Run("test close with api logging enabled", func(t *testing.T) {
		port, err := freeport.GetFreePort()
		require.NoError(t, err)
		serverConfig := ServerConfig{
			AppID:              "test",
			HostAddress:        "127.0.0.1",
			Port:               port,
			APIListenAddresses: []string{"127.0.0.1"},
			MaxRequestBodySize: 4 << 20,
			ReadBufferSize:     4 << 10,
			EnableAPILogging:   true,
		}
		a := &api{}
		server := NewServer(NewServerOpts{
			API:         a,
			Config:      serverConfig,
			TracingSpec: config.TracingSpec{},
			MetricSpec:  config.MetricSpec{},
			Middleware:  func(n http.Handler) http.Handler { return n },
			APISpec:     config.APISpec{},
		})
		require.NoError(t, server.StartNonBlocking())
		dapr_testing.WaitForListeningAddress(t, 5*time.Second, fmt.Sprintf("127.0.0.1:%d", port))
		require.NoError(t, server.Close())
	})

	t.Run("test close with api logging disabled", func(t *testing.T) {
		port, err := freeport.GetFreePort()
		require.NoError(t, err)
		serverConfig := ServerConfig{
			AppID:              "test",
			HostAddress:        "127.0.0.1",
			Port:               port,
			APIListenAddresses: []string{"127.0.0.1"},
			MaxRequestBodySize: 4 << 20,
			ReadBufferSize:     4 << 10,
			EnableAPILogging:   false,
		}
		a := &api{}
		server := NewServer(NewServerOpts{
			API:         a,
			Config:      serverConfig,
			TracingSpec: config.TracingSpec{},
			MetricSpec:  config.MetricSpec{},
			Middleware:  func(n http.Handler) http.Handler { return n },
			APISpec:     config.APISpec{},
		})
		require.NoError(t, server.StartNonBlocking())
		dapr_testing.WaitForListeningAddress(t, 5*time.Second, fmt.Sprintf("127.0.0.1:%d", port))
		require.NoError(t, server.Close())
	})
}
