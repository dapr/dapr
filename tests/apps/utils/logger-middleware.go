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

		ctx := context.WithValue(r.Context(), "reqid", reqID) //nolint:revive,staticcheck

		log.Printf("Received request %s %s (source=%s, reqID=%s)", r.Method, r.URL.Path, r.RemoteAddr, reqID)

		// Process the request
		start := time.Now()
		next.ServeHTTP(w, r.WithContext(ctx))
		dur := time.Now().Sub(start)
		log.Printf("Request %s: completed in %s", reqID, dur)
	})
}
