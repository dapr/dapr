// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/phayes/freeport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"

	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/cors"
	http_middleware "github.com/dapr/dapr/pkg/middleware/http"
	dapr_testing "github.com/dapr/dapr/pkg/testing"
)

type mockHost struct {
	hasCORS bool
}

const healthzEndpoint = "healthz"

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

func TestAllowedAPISpec(t *testing.T) {
	t.Run("state allowed", func(t *testing.T) {
		s := server{
			apiSpec: config.APISpec{
				Allowed: []config.APIAccessRule{
					{
						Name:     "state",
						Version:  "v1.0",
						Protocol: "http",
					},
					{
						Name:     "state",
						Version:  "v1.0-alpha1",
						Protocol: "http",
					},
				},
			},
		}

		a := &api{}
		eps := a.constructStateEndpoints()

		for _, e := range eps {
			valid := s.endpointAllowed(e)
			assert.True(t, valid)
		}

		allOtherEndpoints := []Endpoint{}
		allOtherEndpoints = append(allOtherEndpoints, a.constructActorEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructBindingsEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructDirectMessagingEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructMetadataEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructPubSubEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructSecretEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructShutdownEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructHealthzEndpoints()...)

		for _, e := range allOtherEndpoints {
			valid := s.endpointAllowed(e)
			if e.Route == healthzEndpoint {
				assert.True(t, valid)
			} else {
				assert.False(t, valid)
			}
		}
	})

	t.Run("publish allowed", func(t *testing.T) {
		s := server{
			apiSpec: config.APISpec{
				Allowed: []config.APIAccessRule{
					{
						Name:     "publish",
						Version:  "v1.0",
						Protocol: "http",
					},
				},
			},
		}

		a := &api{}
		eps := a.constructPubSubEndpoints()

		for _, e := range eps {
			valid := s.endpointAllowed(e)
			assert.True(t, valid)
		}

		allOtherEndpoints := []Endpoint{}
		allOtherEndpoints = append(allOtherEndpoints, a.constructActorEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructBindingsEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructDirectMessagingEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructMetadataEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructStateEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructSecretEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructShutdownEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructHealthzEndpoints()...)

		for _, e := range allOtherEndpoints {
			valid := s.endpointAllowed(e)
			if e.Route == healthzEndpoint {
				assert.True(t, valid)
			} else {
				assert.False(t, valid)
			}
		}
	})

	t.Run("invoke allowed", func(t *testing.T) {
		s := server{
			apiSpec: config.APISpec{
				Allowed: []config.APIAccessRule{
					{
						Name:     "invoke",
						Version:  "v1.0",
						Protocol: "http",
					},
				},
			},
		}

		a := &api{}
		eps := a.constructDirectMessagingEndpoints()

		for _, e := range eps {
			valid := s.endpointAllowed(e)
			assert.True(t, valid)
		}

		allOtherEndpoints := []Endpoint{}
		allOtherEndpoints = append(allOtherEndpoints, a.constructActorEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructBindingsEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructPubSubEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructMetadataEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructStateEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructSecretEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructShutdownEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructHealthzEndpoints()...)

		for _, e := range allOtherEndpoints {
			valid := s.endpointAllowed(e)
			if e.Route == healthzEndpoint {
				assert.True(t, valid)
			} else {
				assert.False(t, valid)
			}
		}
	})

	t.Run("bindings allowed", func(t *testing.T) {
		s := server{
			apiSpec: config.APISpec{
				Allowed: []config.APIAccessRule{
					{
						Name:     "bindings",
						Version:  "v1.0",
						Protocol: "http",
					},
				},
			},
		}

		a := &api{}
		eps := a.constructBindingsEndpoints()

		for _, e := range eps {
			valid := s.endpointAllowed(e)
			assert.True(t, valid)
		}

		allOtherEndpoints := []Endpoint{}
		allOtherEndpoints = append(allOtherEndpoints, a.constructActorEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructDirectMessagingEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructPubSubEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructMetadataEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructStateEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructSecretEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructShutdownEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructHealthzEndpoints()...)

		for _, e := range allOtherEndpoints {
			valid := s.endpointAllowed(e)
			if e.Route == healthzEndpoint {
				assert.True(t, valid)
			} else {
				assert.False(t, valid)
			}
		}
	})

	t.Run("metadata allowed", func(t *testing.T) {
		s := server{
			apiSpec: config.APISpec{
				Allowed: []config.APIAccessRule{
					{
						Name:     "metadata",
						Version:  "v1.0",
						Protocol: "http",
					},
				},
			},
		}

		a := &api{}
		eps := a.constructMetadataEndpoints()

		for _, e := range eps {
			valid := s.endpointAllowed(e)
			assert.True(t, valid)
		}

		allOtherEndpoints := []Endpoint{}
		allOtherEndpoints = append(allOtherEndpoints, a.constructActorEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructDirectMessagingEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructPubSubEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructBindingsEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructStateEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructSecretEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructShutdownEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructHealthzEndpoints()...)

		for _, e := range allOtherEndpoints {
			valid := s.endpointAllowed(e)
			if e.Route == healthzEndpoint {
				assert.True(t, valid)
			} else {
				assert.False(t, valid)
			}
		}
	})

	t.Run("secrets allowed", func(t *testing.T) {
		s := server{
			apiSpec: config.APISpec{
				Allowed: []config.APIAccessRule{
					{
						Name:     "secrets",
						Version:  "v1.0",
						Protocol: "http",
					},
				},
			},
		}

		a := &api{}
		eps := a.constructSecretEndpoints()

		for _, e := range eps {
			valid := s.endpointAllowed(e)
			assert.True(t, valid)
		}

		allOtherEndpoints := []Endpoint{}
		allOtherEndpoints = append(allOtherEndpoints, a.constructActorEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructDirectMessagingEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructPubSubEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructBindingsEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructStateEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructMetadataEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructShutdownEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructHealthzEndpoints()...)

		for _, e := range allOtherEndpoints {
			valid := s.endpointAllowed(e)
			if e.Route == healthzEndpoint {
				assert.True(t, valid)
			} else {
				assert.False(t, valid)
			}
		}
	})

	t.Run("shutdown allowed", func(t *testing.T) {
		s := server{
			apiSpec: config.APISpec{
				Allowed: []config.APIAccessRule{
					{
						Name:     "shutdown",
						Version:  "v1.0",
						Protocol: "http",
					},
				},
			},
		}

		a := &api{}
		eps := a.constructShutdownEndpoints()

		for _, e := range eps {
			valid := s.endpointAllowed(e)
			assert.True(t, valid)
		}

		allOtherEndpoints := []Endpoint{}
		allOtherEndpoints = append(allOtherEndpoints, a.constructActorEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructDirectMessagingEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructPubSubEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructBindingsEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructStateEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructMetadataEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructSecretEndpoints()...)
		allOtherEndpoints = append(allOtherEndpoints, a.constructHealthzEndpoints()...)

		for _, e := range allOtherEndpoints {
			valid := s.endpointAllowed(e)
			if e.Route == healthzEndpoint {
				assert.True(t, valid)
			} else {
				assert.False(t, valid)
			}
		}
	})

	t.Run("no rules, all endpoints allowed", func(t *testing.T) {
		s := server{}

		a := &api{}
		eps := a.APIEndpoints()

		for _, e := range eps {
			valid := s.endpointAllowed(e)
			assert.True(t, valid)
		}
	})

	t.Run("router handler no rules, all handlers exist", func(t *testing.T) {
		s := server{}

		a := &api{}
		eps := a.APIEndpoints()

		router := s.getRouter(eps)
		r := &fasthttp.RequestCtx{
			Request: fasthttp.Request{},
		}

		for _, e := range eps {
			path := fmt.Sprintf("/%s/%s", e.Version, e.Route)
			for _, m := range e.Methods {
				handler, ok := router.Lookup(m, path, r)
				assert.NotNil(t, handler)
				assert.True(t, ok)
			}
		}
	})

	t.Run("router handler mismatch protocol, all handlers exist", func(t *testing.T) {
		s := server{
			apiSpec: config.APISpec{
				Allowed: []config.APIAccessRule{
					{
						Name:     "state",
						Version:  "v1.0",
						Protocol: "grpc",
					},
				},
			},
		}

		a := &api{}
		eps := a.APIEndpoints()

		router := s.getRouter(eps)
		r := &fasthttp.RequestCtx{
			Request: fasthttp.Request{},
		}

		for _, e := range eps {
			path := fmt.Sprintf("/%s/%s", e.Version, e.Route)
			for _, m := range e.Methods {
				handler, ok := router.Lookup(m, path, r)
				assert.NotNil(t, handler)
				assert.True(t, ok)
			}
		}
	})

	t.Run("router handler rules applied, only handlers exist", func(t *testing.T) {
		s := server{
			apiSpec: config.APISpec{
				Allowed: []config.APIAccessRule{
					{
						Version:  "v1.0",
						Name:     "state",
						Protocol: "http",
					},
				},
			},
		}

		a := &api{}
		eps := a.APIEndpoints()

		router := s.getRouter(eps)
		r := &fasthttp.RequestCtx{
			Request: fasthttp.Request{},
		}

		for _, e := range eps {
			path := fmt.Sprintf("/%s/%s", e.Version, e.Route)
			for _, m := range e.Methods {
				handler, ok := router.Lookup(m, path, r)

				if strings.Index(e.Route, "state") == 0 {
					assert.NotNil(t, handler)
					assert.True(t, ok)
				} else {
					assert.Nil(t, handler)
					assert.False(t, ok)
				}
			}
		}
	})
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

func TestClose(t *testing.T) {
	port, err := freeport.GetFreePort()
	require.NoError(t, err)
	serverConfig := NewServerConfig("test", "127.0.0.1", port, []string{"127.0.0.1"}, nil, 0, "", false, 4, "", 4, false)
	a := &api{}
	server := NewServer(a, serverConfig, config.TracingSpec{}, config.MetricSpec{}, http_middleware.Pipeline{}, config.APISpec{})
	require.NoError(t, server.StartNonBlocking())
	dapr_testing.WaitForListeningAddress(t, 5*time.Second, fmt.Sprintf("127.0.0.1:%d", port))
	assert.NoError(t, server.Close())
}
