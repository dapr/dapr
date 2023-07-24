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
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

const (
	tlsCertEnvKey = "DAPR_TESTS_TLS_CERT"
	tlsKeyEnvKey  = "DAPR_TESTS_TLS_KEY"
)

// StartServer starts a HTTP or HTTP2 server
func StartServer(port int, appRouter func() http.Handler, allowHTTP2 bool, enableTLS bool) {
	// HTTP/2 is allowed only if the DAPR_TESTS_HTTP2 env var is set
	allowHTTP2 = allowHTTP2 && IsTruthy(os.Getenv("DAPR_TESTS_HTTP2"))

	logConnState := IsTruthy(os.Getenv("DAPR_TESTS_LOG_CONNSTATE"))

	// Create a listener
	addr := fmt.Sprintf(":%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("Failed to create listener: %v", err)
	}

	var server *http.Server
	if allowHTTP2 {
		// Create a server capable of supporting HTTP2 Cleartext connections
		// Also supports HTTP1.1 and upgrades from HTTP1.1 to HTTP2
		h2s := &http2.Server{}
		//nolint:gosec
		server = &http.Server{
			Addr:    addr,
			Handler: h2c.NewHandler(appRouter(), h2s),
			ConnState: func(c net.Conn, cs http.ConnState) {
				if logConnState {
					log.Printf("ConnState changed: %s -> %s state: %s (HTTP2)", c.RemoteAddr(), c.LocalAddr(), cs)
				}
			},
		}
	} else {
		//nolint:gosec
		server = &http.Server{
			Addr:    addr,
			Handler: appRouter(),
			ConnState: func(c net.Conn, cs http.ConnState) {
				if logConnState {
					log.Printf("ConnState changed: %s -> %s state: %s", c.RemoteAddr(), c.LocalAddr(), cs)
				}
			},
		}
	}

	var certFile, keyFile string
	if enableTLS {
		certFile, keyFile, err = getTLSCertAndKey()
		if err != nil {
			log.Fatalf("Failed to get TLS cert and key: %v", err)
		}
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
	if enableTLS {
		err = server.ServeTLS(ln, certFile, keyFile)
	} else {
		err = server.Serve(ln)
	}

	if err != http.ErrServerClosed {
		log.Fatalf("Failed to run server: %v", err)
	}

	log.Println("Server shut down")
}

func getTLSCertAndKey() (string, string, error) {
	cert, ok := os.LookupEnv(tlsCertEnvKey)
	if !ok {
		return "", "", fmt.Errorf("%s is not set", tlsCertEnvKey)
	}
	key, ok := os.LookupEnv(tlsKeyEnvKey)
	if !ok {
		return "", "", fmt.Errorf("%s is not set", tlsKeyEnvKey)
	}
	return cert, key, nil
}
