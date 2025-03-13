package http

/*!
Adapted from the Go 1.19.2 source code
Copyright 2009 The Go Authors. All rights reserved.
License: BSD (https://github.com/golang/go/blob/go1.19.2/LICENSE)
*/

import (
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"

	"golang.org/x/net/http/httpguts"
)

type RWRecorder struct {
	statusCode int
	h          http.Header
	W          io.ReadWriter
}

func (w *RWRecorder) StatusCode() int {
	if w.statusCode == 0 {
		return http.StatusOK
	}
	return w.statusCode
}

func (w *RWRecorder) Header() http.Header {
	if w.h == nil {
		w.h = make(http.Header)
	}
	return w.h
}

func (w *RWRecorder) WriteHeader(code int) {
	if code < 100 || code > 999 {
		panic(fmt.Sprintf("invalid WriteHeader code %v", code))
	}

	w.statusCode = code
}

func (w *RWRecorder) Write(p []byte) (int, error) {
	return w.W.Write(p)
}

func (w *RWRecorder) Result() *http.Response {
	res := &http.Response{
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Body:       io.NopCloser(w.W),
		StatusCode: w.StatusCode(),
		Header:     w.h,
	}

	res.Status = fmt.Sprintf("%03d %s", res.StatusCode, http.StatusText(res.StatusCode))
	res.ContentLength = parseContentLength(res.Header.Get("Content-Length"))

	if trailers, ok := w.h["Trailer"]; ok {
		res.Trailer = make(http.Header, len(trailers))
		for _, k := range trailers {
			for _, k := range strings.Split(k, ",") {
				k = http.CanonicalHeaderKey(textproto.TrimString(k))
				if !httpguts.ValidTrailerHeader(k) {
					// Ignore since forbidden by RFC 7230, section 4.1.2.
					continue
				}
				vv, ok := w.h[k]
				if !ok {
					continue
				}
				vv2 := make([]string, len(vv))
				copy(vv2, vv)
				res.Trailer[k] = vv2
			}
		}
	}

	for k, vv := range w.h {
		if !strings.HasPrefix(k, http.TrailerPrefix) {
			continue
		}
		if res.Trailer == nil {
			res.Trailer = make(http.Header)
		}
		for _, v := range vv {
			res.Trailer.Add(strings.TrimPrefix(k, http.TrailerPrefix), v)
		}
	}
	return res
}

// parseContentLength trims whitespace from s and returns -1 if no value
// is set, or the value if it's >= 0.
func parseContentLength(cl string) int64 {
	cl = textproto.TrimString(cl)
	if cl == "" {
		return -1
	}
	n, err := strconv.ParseUint(cl, 10, 63)
	if err != nil {
		return -1
	}
	return int64(n)
}
