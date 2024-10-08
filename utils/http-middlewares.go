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

package utils

import (
	"io"
	"net/http"

	"github.com/dapr/kit/streams"
)

// UppercaseRequestMiddleware is a HTTP middleware that transforms the request body to uppercase
func UppercaseRequestMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = io.NopCloser(streams.UppercaseTransformer(r.Body))
		next.ServeHTTP(w, r)
	})
}

// UppercaseResponseMiddleware is a HTTP middleware that transforms the response body to uppercase
func UppercaseResponseMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		urw := &uppercaseResponseWriter{w}
		next.ServeHTTP(urw, r)
	})
}

type uppercaseResponseWriter struct {
	http.ResponseWriter
}

func (urw *uppercaseResponseWriter) Write(p []byte) (n int, err error) {
	var written int
	for _, c := range string(p) {
		written, err = urw.ResponseWriter.Write(
			streams.RuneToUppercase(c),
		)
		n += written
		if err != nil {
			return n, err
		}
	}
	return n, nil
}
