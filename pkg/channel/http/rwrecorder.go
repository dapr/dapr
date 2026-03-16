package http

import (
	"fmt"
	"io"
	"net/http"
	"sync"
)

// rwRecorder writes response body data to an io.Writer and signals when
// headers and status code are available.
type rwRecorder struct {
	statusCode int
	h          http.Header
	pw         io.Writer
	readyCh    chan struct{}
	readyOnce  sync.Once
}

func (r *rwRecorder) signalReady() {
	r.readyOnce.Do(func() { close(r.readyCh) })
}

func (r *rwRecorder) StatusCode() int {
	if r.statusCode == 0 {
		return http.StatusOK
	}
	return r.statusCode
}

func (r *rwRecorder) Header() http.Header {
	if r.h == nil {
		r.h = make(http.Header)
	}
	return r.h
}

func (r *rwRecorder) WriteHeader(code int) {
	if code < 100 || code > 999 {
		panic(fmt.Sprintf("invalid WriteHeader code %v", code))
	}
	r.statusCode = code
	r.signalReady()
}

func (r *rwRecorder) Write(p []byte) (int, error) {
	// Implicit 200 status if WriteHeader hasn't been called yet.
	r.signalReady()
	return r.pw.Write(p)
}
