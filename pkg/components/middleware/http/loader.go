// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

import (
	"strings"

	http_middleware "github.com/dapr/dapr/pkg/middleware/http"
	"github.com/valyala/fasthttp"
)

// Load message buses
func Load() {
	RegisterMiddleware("uppercase", func() http_middleware.Middleware {
		return func(h fasthttp.RequestHandler) fasthttp.RequestHandler {
			return func(ctx *fasthttp.RequestCtx) {
				body := string(ctx.PostBody())
				ctx.Request.SetBody([]byte(strings.ToUpper(body)))
				h(ctx)
			}
		}
	})
}
