// Package fasthttpadaptor provides helper functions for converting net/http
// request handlers to fasthttp request handlers.
package fasthttpadaptor

import (
	"io"
	"net/http"
	"net/url"

	"github.com/valyala/fasthttp"
)

// NewFastHTTPHandlerFunc wraps net/http handler func to fasthttp
// request handler, so it can be passed to fasthttp server.
//
// While this function may be used for easy switching from net/http to fasthttp,
// it has the following drawbacks comparing to using manually written fasthttp
// request handler:
//
//     * A lot of useful functionality provided by fasthttp is missing
//       from net/http handler.
//     * net/http -> fasthttp handler conversion has some overhead,
//       so the returned handler will be always slower than manually written
//       fasthttp handler.
//
// So it is advisable using this function only for quick net/http -> fasthttp
// switching. Then manually convert net/http handlers to fasthttp handlers
// according to https://github.com/valyala/fasthttp#switching-from-nethttp-to-fasthttp .
func NewFastHTTPHandlerFunc(h http.HandlerFunc) fasthttp.RequestHandler {
	return NewFastHTTPHandler(h)
}

// NewFastHTTPHandler wraps net/http handler to fasthttp request handler,
// so it can be passed to fasthttp server.
//
// While this function may be used for easy switching from net/http to fasthttp,
// it has the following drawbacks comparing to using manually written fasthttp
// request handler:
//
//     * A lot of useful functionality provided by fasthttp is missing
//       from net/http handler.
//     * net/http -> fasthttp handler conversion has some overhead,
//       so the returned handler will be always slower than manually written
//       fasthttp handler.
//
// So it is advisable using this function only for quick net/http -> fasthttp
// switching. Then manually convert net/http handlers to fasthttp handlers
// according to https://github.com/valyala/fasthttp#switching-from-nethttp-to-fasthttp .
func NewFastHTTPHandler(h http.Handler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		var r http.Request

		body := ctx.PostBody()
		r.Method = string(ctx.Method())
		r.Proto = "HTTP/1.1"
		r.ProtoMajor = 1
		r.ProtoMinor = 1
		r.RequestURI = string(ctx.RequestURI())
		r.ContentLength = int64(len(body))
		r.Host = string(ctx.Host())
		r.RemoteAddr = ctx.RemoteAddr().String()

		hdr := make(http.Header)
		ctx.Request.Header.VisitAll(func(k, v []byte) {
			sk := string(k)
			sv := string(v)
			switch sk {
			case "Transfer-Encoding":
				r.TransferEncoding = append(r.TransferEncoding, sv)
			default:
				hdr.Set(sk, sv)
			}
		})
		r.Header = hdr
		r.Body = &netHTTPBody{body}
		rURL, err := url.ParseRequestURI(r.RequestURI)
		if err != nil {
			ctx.Logger().Printf("cannot parse requestURI %q: %s", r.RequestURI, err)
			ctx.Error("Internal Server Error", fasthttp.StatusInternalServerError)
			return
		}
		r.URL = rURL

		var w netHTTPResponseWriter
		h.ServeHTTP(&w, &r)

		ctx.SetStatusCode(w.StatusCode())
		for k, vv := range w.Header() {
			for _, v := range vv {
				ctx.Response.Header.Set(k, v)
			}
		}
		ctx.Write(w.body)
	}
}

type netHTTPBody struct {
	b []byte
}

func (r *netHTTPBody) Read(p []byte) (int, error) {
	if len(r.b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.b)
	r.b = r.b[n:]
	return n, nil
}

func (r *netHTTPBody) Close() error {
	r.b = r.b[:0]
	return nil
}

type netHTTPResponseWriter struct {
	statusCode int
	h          http.Header
	body       []byte
}

func (w *netHTTPResponseWriter) StatusCode() int {
	if w.statusCode == 0 {
		return http.StatusOK
	}
	return w.statusCode
}

func (w *netHTTPResponseWriter) Header() http.Header {
	if w.h == nil {
		w.h = make(http.Header)
	}
	return w.h
}

func (w *netHTTPResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
}

func (w *netHTTPResponseWriter) Write(p []byte) (int, error) {
	w.body = append(w.body, p...)
	return len(p), nil
}
