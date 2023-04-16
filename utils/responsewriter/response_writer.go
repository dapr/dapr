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

package responsewriter

import (
	"io"
	"net/http"
)

// ResponseWriter is a wrapper around http.ResponseWriter that provides extra information about
// the response. It is recommended that middleware handlers use this construct to wrap a responsewriter
// if the functionality calls for it.
type ResponseWriter interface {
	http.ResponseWriter

	// Status returns the status code of the response or 0 if the response has
	// not been written
	Status() int
	// Written returns whether or not the ResponseWriter has been written.
	Written() bool
	// Size returns the size of the response body.
	Size() int
	// Before allows for a function to be called before the ResponseWriter has been written to. This is
	// useful for setting headers or any other operations that must happen before a response has been written.
	Before(func(ResponseWriter))
	// UserValue retrieves values from the object.
	UserValue(key any) any
	// UserValueString retrieves the user value and casts it as string.
	// If the value is not a string, returns an empty string.
	UserValueString(key any) string
	// AllUserValues retrieves all user values.
	AllUserValues() map[any]any
	// SetUserValue sets arbitrary values in the object.
	SetUserValue(key any, value any)
}

type beforeFunc func(ResponseWriter)

// NewResponseWriter creates a ResponseWriter that wraps a http.ResponseWriter
func NewResponseWriter(rw http.ResponseWriter) ResponseWriter {
	return &responseWriter{
		ResponseWriter: rw,
	}
}

// EnsureResponseWriter creates a ResponseWriter that wraps a http.ResponseWriter, unless it's already a ResponseWriter.
func EnsureResponseWriter(rw http.ResponseWriter) ResponseWriter {
	rwObj, ok := rw.(ResponseWriter)
	if ok {
		return rwObj
	}

	return NewResponseWriter(rw)
}

type responseWriter struct {
	http.ResponseWriter
	pendingStatus  int
	status         int
	size           int
	beforeFuncs    []beforeFunc
	callingBefores bool
	userValues     map[any]any
}

func (rw *responseWriter) WriteHeader(s int) {
	if rw.Written() {
		return
	}

	rw.pendingStatus = s
	rw.callBefore()

	// Any of the rw.beforeFuncs may have written a header,
	// so check again to see if any work is necessary.
	if rw.Written() {
		return
	}

	rw.status = s
	rw.ResponseWriter.WriteHeader(s)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.Written() {
		// The status will be StatusOK if WriteHeader has not been called yet
		rw.WriteHeader(http.StatusOK)
	}
	size, err := rw.ResponseWriter.Write(b)
	rw.size += size
	return size, err
}

// ReadFrom exposes underlying http.ResponseWriter to io.Copy and if it implements
// io.ReaderFrom, it can take advantage of optimizations such as sendfile, io.Copy
// with sync.Pool's buffer which is in http.(*response).ReadFrom and so on.
func (rw *responseWriter) ReadFrom(r io.Reader) (n int64, err error) {
	if !rw.Written() {
		// The status will be StatusOK if WriteHeader has not been called yet
		rw.WriteHeader(http.StatusOK)
	}
	n, err = io.Copy(rw.ResponseWriter, r)
	rw.size += int(n)
	return
}

// Satisfy http.ResponseController support (Go 1.20+)
func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

func (rw *responseWriter) Status() int {
	if rw.Written() {
		return rw.status
	}

	return rw.pendingStatus
}

func (rw *responseWriter) Size() int {
	return rw.size
}

func (rw *responseWriter) Written() bool {
	return rw.status != 0
}

func (rw *responseWriter) Before(before func(ResponseWriter)) {
	rw.beforeFuncs = append(rw.beforeFuncs, before)
}

func (rw *responseWriter) callBefore() {
	// Don't recursively call before() functions, to avoid infinite looping if
	// one of them calls rw.WriteHeader again.
	if rw.callingBefores {
		return
	}

	rw.callingBefores = true
	defer func() { rw.callingBefores = false }()

	for i := len(rw.beforeFuncs) - 1; i >= 0; i-- {
		rw.beforeFuncs[i](rw)
	}
}

func (rw *responseWriter) SetUserValue(key any, value any) {
	if rw.userValues == nil {
		rw.userValues = map[any]any{}
	}
	rw.userValues[key] = value
}

func (rw *responseWriter) UserValue(key any) any {
	if rw.userValues == nil {
		return nil
	}
	return rw.userValues[key]
}

func (rw *responseWriter) UserValueString(key any) string {
	v, _ := rw.UserValue(key).(string)
	return v
}

func (rw *responseWriter) AllUserValues() map[any]any {
	return rw.userValues
}
