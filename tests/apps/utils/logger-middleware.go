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
	"context"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// LoggerMiddleware returns a middleware for gorilla/mux that logs all requests and processing times
func LoggerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if we have a request ID or generate one
		reqID := r.URL.Query().Get("reqid")
		if reqID == "" {
			reqID = r.Header.Get("x-daprtest-reqid")
		}
		if reqID == "" {
			reqID = "m-" + uuid.New().String()
		}

		ctx := context.WithValue(r.Context(), "reqid", reqID) //nolint:staticcheck

		u := r.URL.Path
		qs := r.URL.Query().Encode()
		if qs != "" {
			u += "?" + qs
		}
		log.Printf("Received request %s %s (source=%s, reqID=%s)", r.Method, u, r.RemoteAddr, reqID)

		// Process the request
		start := time.Now()
		next.ServeHTTP(w, r.WithContext(ctx))
		log.Printf("Request %s: completed in %s", reqID, time.Since(start))
	})
}
