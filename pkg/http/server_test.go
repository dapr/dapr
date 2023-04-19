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

//nolint:forbidigo
package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/phayes/freeport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"

	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/cors"
	httpMiddleware "github.com/dapr/dapr/pkg/middleware/http"
	dapr_testing "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/logger"
)

type mockHost struct {
	hasCORS bool
}

func (m *mockHost) mockHandler() fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		b := ctx.Response.Header.Peek("Access-Control-Allow-Origin")
		m.hasCORS = len(b) > 0
	}
}

func newServer() server {
	return server{
		config: ServerConfig{},
	}
}

func TestCorsHandler(t *testing.T) {
	t.Run("with default cors, middleware not enabled", func(t *testing.T) {
		srv := newServer()
		srv.config.AllowedOrigins = cors.DefaultAllowedOrigins

		mh := mockHost{}
		h := srv.useCors(mh.mockHandler())
		r := &fasthttp.RequestCtx{
			Request: fasthttp.Request{},
		}
		r.Request.Header.Set("Origin", "*")
		h(r)

		assert.False(t, mh.hasCORS)
	})

	t.Run("with custom cors, middleware enabled", func(t *testing.T) {
		srv := newServer()
		srv.config.AllowedOrigins = "http://test.com"

		mh := mockHost{}
		h := srv.useCors(mh.mockHandler())
		r := &fasthttp.RequestCtx{
			Request: fasthttp.Request{},
		}
		r.Request.Header.Set("Origin", "http://test.com")
		h(r)
		assert.True(t, mh.hasCORS)
	})
}

func TestUnescapeRequestParametersHandler(t *testing.T) {
	mh := func(reqCtx *fasthttp.RequestCtx) {
		pc, _, _, ok := runtime.Caller(1)
		if !ok {
			reqCtx.Response.SetBody([]byte("error"))
		} else {
			handlerFunctionName := runtime.FuncForPC(pc).Name()
			fmt.Println(handlerFunctionName)
			reqCtx.Response.SetBody([]byte(handlerFunctionName))
		}
	}
	t.Run("unescapeRequestParametersHandler is added as middleware if the endpoint includes Parameters in its path", func(t *testing.T) {
		endpoints := []Endpoint{
			{
				Methods: []string{fasthttp.MethodGet},
				Route:   "state/{storeName}/{key}",
				Version: apiVersionV1,
				Handler: mh,
			},
			{
				Methods: []string{fasthttp.MethodGet},
				Route:   "secrets/{secretStoreName}/{key}",
				Version: apiVersionV1,
				Handler: mh,
			},
			{
				Methods: []string{fasthttp.MethodPost, fasthttp.MethodPut},
				Route:   "publish/{pubsubname}/{topic:*}",
				Version: apiVersionV1,
				Handler: mh,
			},
			{
				Methods: []string{fasthttp.MethodGet, fasthttp.MethodPost, fasthttp.MethodDelete, fasthttp.MethodPut},
				Route:   "actors/{actorType}/{actorId}/method/{method}",
				Version: apiVersionV1,
				Handler: mh,
			},
		}
		srv := newServer()
		router := srv.getRouter(endpoints)
		r := &fasthttp.RequestCtx{
			Request: fasthttp.Request{},
		}
		for _, e := range endpoints {
			path := fmt.Sprintf("/%s/%s", e.Version, e.Route)
			for _, m := range e.Methods {
				handler, _ := router.Lookup(m, path, r)
				handler(r)
				handlerFunctionName := string(r.Response.Body())
				assert.Contains(t, handlerFunctionName, "unescapeRequestParametersHandler")
			}
		}
	})
	t.Run("unescapeRequestParameterHandler is not added as middleware if the endpoint does not include Parameters in its path", func(t *testing.T) {
		endpoints := []Endpoint{
			{
				Methods: []string{fasthttp.MethodGet},
				Route:   "metadata",
				Version: apiVersionV1,
				Handler: mh,
			},
			{
				Methods: []string{fasthttp.MethodGet},
				Route:   "healthz",
				Version: apiVersionV1,
				Handler: mh,
			},
		}
		srv := newServer()
		router := srv.getRouter(endpoints)
		r := &fasthttp.RequestCtx{
			Request: fasthttp.Request{},
		}
		for _, e := range endpoints {
			path := fmt.Sprintf("/%s/%s", e.Version, e.Route)
			for _, m := range e.Methods {
				handler, _ := router.Lookup(m, path, r)
				handler(r)
				handlerFunctionName := string(r.Response.Body())
				assert.Contains(t, handlerFunctionName, "TestUnescapeRequestParametersHandler")
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
		h := srv.unescapeRequestParametersHandler(mh)
		for _, parameter := range parameters {
			r := &fasthttp.RequestCtx{
				Request: fasthttp.Request{},
			}
			r.SetUserValue(parameter["parameterName"], parameter["parameterValue"])
			h(r)
			newParameterValue := r.UserValue(parameter["parameterName"])
			assert.Equal(t, parameter["expectedParameterValue"], newParameterValue)
		}
	})
	t.Run("Unescape invalid Parameters", func(t *testing.T) {
		parameters := []map[string]string{
			{
				"parameterName":         "stateStore",
				"parameterValue":        "unknown%2state%20store",
				"expectedUnescapeError": "\"%2s\"",
			},
			{
				"parameterName":         "stateStore",
				"parameterValue":        "stateStore%2prod",
				"expectedUnescapeError": "\"%2p\"",
			},
			{
				"parameterName":         "secretStore",
				"parameterValue":        "unknown%20secret%2store",
				"expectedUnescapeError": "\"%2s\"",
			},
		}
		srv := newServer()
		h := srv.unescapeRequestParametersHandler(mh)
		for _, parameter := range parameters {
			r := &fasthttp.RequestCtx{
				Request: fasthttp.Request{},
			}
			r.SetUserValue(parameter["parameterName"], parameter["parameterValue"])
			h(r)
			expectedErrorMessage := fmt.Sprintf("Failed to unescape request parameter %s with value %s. Error: invalid URL escape %s", parameter["parameterName"], parameter["parameterValue"], parameter["expectedUnescapeError"])
			responseStatusCode := r.Response.StatusCode()
			errorMessage := string(r.Response.Body())
			assert.Equal(t, errorMessage, expectedErrorMessage)
			assert.Equal(t, responseStatusCode, fasthttp.StatusBadRequest)
		}
	})
}

func TestAliasRoute(t *testing.T) {
	t.Run("When direct messaging has alias endpoint", func(t *testing.T) {
		s := &server{}
		a := &api{}
		eps := a.constructDirectMessagingEndpoints()
		routes := s.getRouter(eps).List()
		assert.Equal(t, 1, len(eps))
		assert.Equal(t, 2, len(routes[router.MethodWild]))
	})

	t.Run("When direct messaging doesn't have alias defined", func(t *testing.T) {
		s := &server{}
		a := &api{}
		eps := a.constructDirectMessagingEndpoints()
		assert.Equal(t, 1, len(eps))
		eps[0].Alias = ""
		routes := s.getRouter(eps).List()
		assert.Equal(t, 1, len(routes[router.MethodWild]))
	})
}

func TestAPILogging(t *testing.T) {
	// Replace the logger with a custom one for testing
	prev := infoLog
	logDest := &bytes.Buffer{}
	infoLog = logger.NewLogger("test-api-logging")
	infoLog.EnableJSONOutput(true)
	infoLog.SetOutput(logDest)
	defer func() {
		infoLog = prev
	}()

	body := []byte("👋")

	mh := func(reqCtx *fasthttp.RequestCtx) {
		reqCtx.Response.SetBody(body)
	}
	endpoints := []Endpoint{
		{
			Methods: []string{fasthttp.MethodGet, fasthttp.MethodPost},
			Route:   "state/{storeName}/{key}",
			Version: apiVersionV1,
			Handler: mh,
		},
	}
	srv := newServer()
	srv.config.EnableAPILogging = true
	router := srv.getRouter(endpoints)

	r := &fasthttp.RequestCtx{
		Request: fasthttp.Request{},
	}
	dec := json.NewDecoder(logDest)

	runTest := func(userAgent string, obfuscateURL bool) func(t *testing.T) {
		return func(t *testing.T) {
			srv.config.APILoggingObfuscateURLs = obfuscateURL
			r.Request.Header.Set("User-Agent", userAgent)

			for _, e := range endpoints {
				routePath := fmt.Sprintf("/%s/%s", e.Version, e.Route)
				path := fmt.Sprintf("/%s/%s", e.Version, "state/mystate/mykey")
				for _, m := range e.Methods {
					handler, _ := router.Lookup(m, path, r)
					r.Request.Header.SetMethod(m)
					r.Request.Header.SetRequestURI(path)
					handler(r)

					assert.Equal(t, body, r.Response.Body())

					logData := map[string]string{}
					err := dec.Decode(&logData)
					require.NoError(t, err)

					assert.Equal(t, "test-api-logging", logData["scope"])
					assert.Equal(t, "HTTP API Called", logData["msg"])
					if obfuscateURL {
						assert.Equal(t, m+" "+routePath, logData["method"])
					} else {
						assert.Equal(t, m+" "+path, logData["method"])
					}
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
	infoLog.SetOutput(logDest)
	defer func() {
		infoLog = prev
	}()

	body := []byte("👋")

	mh := func(reqCtx *fasthttp.RequestCtx) {
		reqCtx.Response.SetBody(body)
	}
	endpoints := []Endpoint{
		{
			Methods:       []string{fasthttp.MethodGet},
			Route:         "log",
			Version:       apiVersionV1,
			Handler:       mh,
			IsHealthCheck: false,
		},
		{
			Methods:       []string{fasthttp.MethodGet},
			Route:         "nolog",
			Version:       apiVersionV1,
			Handler:       mh,
			IsHealthCheck: true,
		},
	}
	srv := newServer()
	srv.config.EnableAPILogging = true
	srv.config.APILogHealthChecks = false
	router := srv.getRouter(endpoints)

	r := &fasthttp.RequestCtx{
		Request: fasthttp.Request{},
	}
	dec := json.NewDecoder(logDest)

	for _, e := range endpoints {
		path := fmt.Sprintf("/%s/%s", e.Version, e.Route)
		handler, _ := router.Lookup(fasthttp.MethodGet, path, r)
		r.Request.Header.SetMethod(fasthttp.MethodGet)
		r.Request.Header.SetRequestURI(path)
		handler(r)

		assert.Equal(t, body, r.Response.Body())

		if e.Route == "log" {
			logData := map[string]string{}
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
			MaxRequestBodySize: 4,
			ReadBufferSize:     4,
			EnableAPILogging:   true,
		}
		a := &api{}
		server := NewServer(NewServerOpts{
			API:         a,
			Config:      serverConfig,
			TracingSpec: config.TracingSpec{},
			MetricSpec:  config.MetricSpec{},
			Pipeline:    httpMiddleware.Pipeline{},
			APISpec:     config.APISpec{},
		})
		require.NoError(t, server.StartNonBlocking())
		dapr_testing.WaitForListeningAddress(t, 5*time.Second, fmt.Sprintf("127.0.0.1:%d", port))
		assert.NoError(t, server.Close())
	})

	t.Run("test close with api logging disabled", func(t *testing.T) {
		port, err := freeport.GetFreePort()
		require.NoError(t, err)
		serverConfig := ServerConfig{
			AppID:              "test",
			HostAddress:        "127.0.0.1",
			Port:               port,
			APIListenAddresses: []string{"127.0.0.1"},
			MaxRequestBodySize: 4,
			ReadBufferSize:     4,
			EnableAPILogging:   false,
		}
		a := &api{}
		server := NewServer(NewServerOpts{
			API:         a,
			Config:      serverConfig,
			TracingSpec: config.TracingSpec{},
			MetricSpec:  config.MetricSpec{},
			Pipeline:    httpMiddleware.Pipeline{},
			APISpec:     config.APISpec{},
		})
		require.NoError(t, server.StartNonBlocking())
		dapr_testing.WaitForListeningAddress(t, 5*time.Second, fmt.Sprintf("127.0.0.1:%d", port))
		assert.NoError(t, server.Close())
	})
}
