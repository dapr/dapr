// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/dapr/dapr/pkg/cors"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
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
