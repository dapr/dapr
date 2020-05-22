package http

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

func TestUseProxy(t *testing.T) {
	eval := func(ctx *fasthttp.RequestCtx) {
		var Forwarded, XForwardedProto bool
		ctx.Request.Header.VisitAll(func(k []byte, v []byte) {
			if strings.EqualFold(string(k), "forwarded") {
				Forwarded = true
			}
			if strings.EqualFold(string(k), "x-forwarded-proto") {
				XForwardedProto = true
			}
		})
		assert.True(t, Forwarded)
		assert.True(t, XForwardedProto)
	}
	s := NewTestServer()
	h := s.useProxy(eval)
	h(&fasthttp.RequestCtx{})
}

func NewTestServer() *server { //nolint:golint
	return &server{}
}
