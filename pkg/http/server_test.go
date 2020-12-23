// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

import (
	"fmt"
	"testing"

	"github.com/dapr/dapr/pkg/cors"
	"github.com/dapr/dapr/pkg/version"
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

func TestUnscapeURIQueryHandler(t *testing.T) {
	mh := func(reqCtx *fasthttp.RequestCtx) {}
	h := unscapeURIQuery(mh)
	t.Run("with valid Request", func(t *testing.T) {
		testRequests := [][2]string{
			{"/%s/state/unknown%%20state%%20store/key", "/%s/state/unknown state store/key"},
			{"/%s/state/unknown%%20state store", "/%s/state/unknown state store"},
			{"/%s/state/unknown state%%20store", "/%s/state/unknown state store"},
			{"/%s/state/unknown state store", "/%s/state/unknown state store"},
			{"/%s/state/statestore%%2Fhello", "/%s/state/statestore/hello"},
		}
		for _, testRequest := range testRequests {
			r := &fasthttp.RequestCtx{
				Request: fasthttp.Request{},
			}
			requestURI := fmt.Sprintf(testRequest[0], version.Version())
			r.Request.SetRequestURI(requestURI)
			h(r)
			newRequestURI := string(r.RequestURI())
			expectedNewRequestURI := fmt.Sprintf(testRequest[1], version.Version())

			assert.Equal(t, expectedNewRequestURI, newRequestURI)
		}
	})

	t.Run("with invalid Request", func(t *testing.T) {
		testRequests := [][2]string{
			{"/%s/state/unknown%%2state%%20store/key", "Failed to unescape request /%s/state/unknown%%2state%%20store/key with error invalid URL escape \"%%2s\""},
			{"/%s/state/unknown%%2state store", "Failed to unescape request /%s/state/unknown%%2state store with error invalid URL escape \"%%2s\""},
			{"/%s/state/unknown state%%2store", "Failed to unescape request /%s/state/unknown state%%2store with error invalid URL escape \"%%2s\""},
		}
		for _, testRequest := range testRequests {
			r := &fasthttp.RequestCtx{
				Request: fasthttp.Request{},
			}
			requestURI := fmt.Sprintf(testRequest[0], version.Version())
			r.Request.SetRequestURI(requestURI)
			h(r)
			requestResponse := string(r.Response.Body())
			requestResponseStatusCode := r.Response.StatusCode()
			expectedResponse := fmt.Sprintf(testRequest[1], version.Version())
			expectedResponseStatusCode := fasthttp.StatusBadRequest

			assert.Equal(t, expectedResponse, requestResponse)
			assert.Equal(t, expectedResponseStatusCode, requestResponseStatusCode)
		}
	})
}
