// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

import (
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
