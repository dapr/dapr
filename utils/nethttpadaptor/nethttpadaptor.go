/*
Copyright 2022 The Dapr Authors
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

package nethttpadaptor

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"

	chi "github.com/go-chi/chi/v5"
	"github.com/valyala/fasthttp"

	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.nethttpadaptor")

// NewNetHTTPHandlerFunc wraps a fasthttp.RequestHandler in a http.HandlerFunc.
func NewNetHTTPHandlerFunc(h fasthttp.RequestHandler) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := fasthttp.RequestCtx{}
		remoteIP := net.ParseIP(r.RemoteAddr)
		remoteAddr := net.IPAddr{
			IP:   remoteIP,
			Zone: "",
		}
		c.Init(&fasthttp.Request{}, &remoteAddr, nil)

		if r.Body != nil {
			reqBody, err := io.ReadAll(r.Body)
			if err != nil {
				msg := fmt.Sprintf("error reading request body: %v", err)
				log.Errorf(msg)
				http.Error(w, msg, http.StatusBadRequest)
				return
			}
			c.Request.SetBody(reqBody)
		}

		// Disable path normalization because we do not use a router after the fasthttp adapter
		c.Request.URI().DisablePathNormalizing = true
		c.Request.URI().SetQueryString(r.URL.RawQuery)
		c.Request.URI().SetPath(r.URL.Path)
		c.Request.URI().SetScheme(r.URL.Scheme)
		c.Request.SetHost(r.Host)
		c.Request.Header.SetMethod(r.Method)
		c.Request.Header.Set("Proto", r.Proto)
		major := strconv.Itoa(r.ProtoMajor)
		minor := strconv.Itoa(r.ProtoMinor)
		c.Request.Header.Set("Protomajor", major)
		c.Request.Header.Set("Protominor", minor)
		c.Request.Header.SetContentType(r.Header.Get("Content-Type"))
		c.Request.Header.SetContentLength(int(r.ContentLength))
		c.Request.Header.SetReferer(r.Referer())
		c.Request.Header.SetUserAgent(r.UserAgent())
		for _, cookie := range r.Cookies() {
			c.Request.Header.SetCookie(cookie.Name, cookie.Value)
		}
		for k, v := range r.Header {
			for _, i := range v {
				c.Request.Header.Add(k, i)
			}
		}

		// Ensure user values are propagated if the context is a fasthttp.RequestCtx already
		if reqCtx, ok := r.Context().(*fasthttp.RequestCtx); ok {
			reqCtx.VisitUserValuesAll(func(k any, v any) {
				c.SetUserValue(k, v)
			})
		}

		// Likewise, if the context is a chi context, propagate the values
		if chiCtx := chi.RouteContext(r.Context()); chiCtx != nil {
			for i, k := range chiCtx.URLParams.Keys {
				c.SetUserValueBytes([]byte(k), chiCtx.URLParams.Values[i])
			}
		}

		// Propagate the context
		span := diagUtils.SpanFromContext(r.Context())
		if span != nil {
			diagUtils.AddSpanToFasthttpContext(&c, span)
		}

		// Invoke the handler
		h(&c)

		c.Response.Header.VisitAll(func(k []byte, v []byte) {
			w.Header().Add(string(k), string(v))
		})
		status := c.Response.StatusCode()
		w.WriteHeader(status)

		c.Response.BodyWriteTo(w)
	})
}
