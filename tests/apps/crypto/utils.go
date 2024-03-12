/*
Copyright 2024 The Dapr Authors
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

package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/google/uuid"
)

/**
This file contains code from tests/e2e/utils (with some simplifications).
It is copied here otherwise we'd have to import dapr/dapr in the Docker container, causing a number of issues.
*/

// NewHTTPClient returns a HTTP client configured
func NewHTTPClient() *http.Client {
	dialer := &net.Dialer{ //nolint:exhaustivestruct
		Timeout: 5 * time.Second,
	}
	netTransport := &http.Transport{ //nolint:exhaustivestruct
		DialContext:         dialer.DialContext,
		TLSHandshakeTimeout: 5 * time.Second,
	}

	return &http.Client{ //nolint:exhaustivestruct
		Timeout:   30 * time.Second,
		Transport: netTransport,
	}
}

// PortFromEnv returns a port from an env var, or the default value.
func PortFromEnv(envName string, defaultPort int) (res int) {
	p := os.Getenv(envName)
	if p != "" && p != "0" {
		res, _ = strconv.Atoi(p)
	}
	if res <= 0 {
		res = defaultPort
	}
	return
}

// StartServer starts a HTTP or HTTP2 server
func StartServer(port int, appRouter func() http.Handler) {
	// Create a listener
	addr := fmt.Sprintf(":%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("Failed to create listener: %v", err)
	}

	//nolint:gosec
	server := &http.Server{
		Addr:    addr,
		Handler: appRouter(),
	}

	// Stop the server when we get a termination signal
	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, syscall.SIGKILL, syscall.SIGTERM, syscall.SIGINT) //nolint:staticcheck
	go func() {
		// Wait for cancelation signal
		<-stopCh
		log.Println("Shutdown signal received")
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	// Blocking call
	if server.Serve(ln) != http.ErrServerClosed {
		log.Fatalf("Failed to run server: %v", err)
	}

	log.Println("Server shut down")
}

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

		log.Printf("Received request %s %s (source=%s, reqID=%s)", r.Method, r.URL.Path, r.RemoteAddr, reqID)

		// Process the request
		start := time.Now()
		next.ServeHTTP(w, r.WithContext(ctx))
		log.Printf("Request %s: completed in %s", reqID, time.Since(start))
	})
}
