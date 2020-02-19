package http

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

func TestUseProxy(t *testing.T) {
	eval := func(ctx *fasthttp.RequestCtx) {
		var Forwarded, XForwardedFor, XForwardedHost, XForwardedProto string
		ctx.Request.Header.VisitAll(func(k []byte, v []byte) {
			if strings.EqualFold(string(k), "forwarded") {
				Forwarded = string(v)
			}
			if strings.EqualFold(string(k), "x-forwarded-for") {
				XForwardedFor = string(v)
			}
			if strings.EqualFold(string(k), "x-forwarded-host") {
				XForwardedHost = string(v)
			}
			if strings.EqualFold(string(k), "x-forwarded-proto") {
				XForwardedProto = string(v)
			}
		})
		assert.True(t, len(Forwarded) > 0)
		assert.True(t, len(XForwardedFor) > 0)
		assert.True(t, len(XForwardedHost) > 0)
		assert.True(t, len(XForwardedProto) > 0)
	}
	s := NewTestServer()
	s.useProxy(eval)
}

func NewTestServer() *server { //nolint:golint
	return &server{}
}
