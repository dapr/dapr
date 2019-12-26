// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

import (
	"strings"

	middleware "github.com/dapr/components-contrib/middleware"
	"github.com/dapr/components-contrib/middleware/http/oauth2"
	http_middleware "github.com/dapr/dapr/pkg/middleware/http"
	"github.com/valyala/fasthttp"
)

// Load HTTP middleware
func Load() {
	RegisterMiddleware("uppercase", func(metadata middleware.Metadata) http_middleware.Middleware {
		return func(h fasthttp.RequestHandler) fasthttp.RequestHandler {
			return func(ctx *fasthttp.RequestCtx) {
				body := string(ctx.PostBody())
				ctx.Request.SetBody([]byte(strings.ToUpper(body)))
				h(ctx)
			}
		}
	})
	RegisterMiddleware("oauth2", func(metadata middleware.Metadata) http_middleware.Middleware {
		handler, _ := oauth2.NewOAuth2Middleware().GetHandler(metadata)
		return handler
	})
}
