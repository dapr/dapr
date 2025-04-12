/*
Copyright 2025 The Dapr Authors
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

package http

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"time"

	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/sentry/server/ca"
	"github.com/dapr/kit/logger"
)

const (
	// JWKSEndpoint is the endpoint that serves the JWKS for JWT validation
	JWKSEndpoint = "/.well-known/jwks.json"
)

var log = logger.NewLogger("dapr.sentry.server.http")

// Options is the configuration options for the HTTP server
type Options struct {
	// Port is the port that the server will listen on
	Port int
	// ListenAddress is the address the server will listen on
	ListenAddress string
	// CABundle is the CA bundle used for TLS
	CABundle ca.Bundle
	// Healthz is the health interface for the server
	Healthz healthz.Healthz
}

// Server is the HTTP server for Sentry
type Server struct {
	port          int
	listenAddress string
	caBundle      ca.Bundle
	htarget       healthz.Target
	server        *http.Server
}

// New creates a new HTTP server with the given options
func New(opts Options) *Server {
	return &Server{
		port:          opts.Port,
		listenAddress: opts.ListenAddress,
		caBundle:      opts.CABundle,
		htarget:       opts.Healthz.AddTarget(),
	}
}

// Start starts the HTTP server and blocks until the context is canceled
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	// Add JWKS endpoint
	mux.HandleFunc(JWKSEndpoint, s.handleJWKS)

	addr := fmt.Sprintf("%s:%d", s.listenAddress, s.port)

	// Create TLS config using the CA bundle
	tlsConfig, err := createTLSConfig(s.caBundle)
	if err != nil {
		return fmt.Errorf("failed to create TLS config for HTTPS server: %w", err)
	}

	s.server = &http.Server{
		Addr:      addr,
		Handler:   mux,
		TLSConfig: tlsConfig,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Infof("Starting HTTPS server on %s", addr)
		// Note: We pass empty strings for cert and key files because we're using the
		// TLS config directly with the loaded certificates
		if err := s.server.ListenAndServeTLS("", ""); err != http.ErrServerClosed {
			errCh <- fmt.Errorf("HTTPS server error: %w", err)
			return
		}
		errCh <- nil
	}()

	// Allow time for the server to start before marking as ready
	time.Sleep(100 * time.Millisecond)
	s.htarget.Ready()

	select {
	case err := <-errCh:
		s.htarget.NotReady()
		return err
	case <-ctx.Done():
		s.htarget.NotReady()
		log.Info("Shutting down HTTPS server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.server.Shutdown(shutdownCtx)
	}
}

// createTLSConfig creates a TLS configuration from the CA bundle
func createTLSConfig(bundle ca.Bundle) (*tls.Config, error) {
	// Load issuer certificate
	certBlock, _ := pem.Decode(bundle.IssChainPEM)
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("failed to decode PEM block containing issuer certificate")
	}

	issuerCert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse issuer certificate: %w", err)
	}

	// Load issuer private key
	keyBlock, _ := pem.Decode(bundle.IssKeyPEM)
	if keyBlock == nil || keyBlock.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("failed to decode PEM block containing private key")
	}

	issuerKey, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse issuer private key: %w", err)
	}

	// Create certificate chain
	certChain := [][]byte{certBlock.Bytes}

	// Create TLS certificate for the server
	tlsCert := tls.Certificate{
		Certificate: certChain,
		PrivateKey:  issuerKey,
		Leaf:        issuerCert,
	}

	return &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// handleJWKS handles requests to the JWKS endpoint
func (s *Server) handleJWKS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.caBundle.JWKS == nil || len(s.caBundle.JWKS) == 0 {
		log.Error("JWKS not available in bundle")
		http.Error(w, "JWKS not available", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)

	// The JWKS is already a marshaled JSON object, so we can write it directly
	if _, err := w.Write(s.caBundle.JWKS); err != nil {
		log.Errorf("Failed to write JWKS response: %v", err)
	}
}

// JWKSResponse is a test utility to parse the JWKS response for validation
func JWKSResponse(jwksBytes []byte) (map[string]interface{}, error) {
	var response map[string]interface{}
	if err := json.Unmarshal(jwksBytes, &response); err != nil {
		return nil, err
	}
	return response, nil
}
