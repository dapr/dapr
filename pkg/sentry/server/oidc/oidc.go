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

package oidc

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"

	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/kit/logger"
)

const (
	// JWKSEndpoint is the endpoint that serves the JWKS for JWT validation
	JWKSEndpoint = "/jwks.json"
	// OIDCDiscoveryEndpoint is the endpoint that serves the OIDC discovery document
	OIDCDiscoveryEndpoint = "/.well-known/openid-configuration"
)

var log = logger.NewLogger("dapr.sentry.server.http")

// Options is the configuration options for the HTTP server
type Options struct {
	// Port is the port that the server will listen on
	Port int
	// ListenAddress is the address the server will listen on
	ListenAddress string
	// JWKS is the JSON Web Key Set (JWKS) for the server
	JWKS []byte
	// Healthz is the health interface for the server
	Healthz healthz.Healthz
	// JWKSURI is the public URI where the JWKS can be accessed (if different from server address)
	JWKSURI string
	// Domains is a list of allowed domains for validation
	Domains []string
	// TLSConfig is an optional custom TLS configuration
	TLSConfig *tls.Config
	// JWTIssuer is the issuer to use for JWT tokens (if not set, issuer not set)
	JWTIssuer string
	// PathPrefix is a prefix to add to all HTTP endpoints
	PathPrefix string
	// SignatureAlgorithm is the signature algorithm to use for JWT tokens
	SignatureAlgorithm jwa.KeyAlgorithm
	// Insecure allows using HTTP instead of HTTPS for the OIDC server and discovery document
	Insecure bool
}

// Server is a HTTP server that partially implements the OIDC spec.
// Its purpose is only to support 3rd party resource providers
// being able to verify the JWT tokens issued by the Sentry server
// which may be used by the Dapr runtime to authenticate to components.
type Server struct {
	port               int
	listenAddress      string
	jwks               []byte
	htarget            healthz.Target
	server             *http.Server
	jwksURI            string
	domains            []string
	tlsConfig          *tls.Config
	jwtIssuer          string
	pathPrefix         string
	signatureAlgorithm jwa.KeyAlgorithm
	insecure           bool
}

// New creates a new HTTP server with the given options
func New(opts Options) *Server {
	if opts.PathPrefix == "" {
		opts.PathPrefix = "/"
	}

	return &Server{
		port:               opts.Port,
		listenAddress:      opts.ListenAddress,
		jwks:               opts.JWKS,
		htarget:            opts.Healthz.AddTarget(),
		jwksURI:            opts.JWKSURI,
		domains:            opts.Domains,
		tlsConfig:          opts.TLSConfig,
		jwtIssuer:          opts.JWTIssuer,
		pathPrefix:         opts.PathPrefix,
		signatureAlgorithm: opts.SignatureAlgorithm,
		insecure:           opts.Insecure,
	}
}

func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	if !s.insecure && s.tlsConfig == nil {
		return fmt.Errorf("TLS configuration is required when not in insecure mode")
	}
	_, err := url.Parse(s.jwksURI)
	if err != nil {
		return fmt.Errorf("invalid JWKS URI: %w", err)
	}
	if s.signatureAlgorithm == nil {
		return fmt.Errorf("signature algorithm is required")
	}

	// Add path prefix to endpoints if configured
	jwksEndpoint := JWKSEndpoint
	oidcEndpoint := OIDCDiscoveryEndpoint
	if s.pathPrefix != "" && s.pathPrefix != "/" {
		if strings.HasSuffix(s.pathPrefix, "/") {
			s.pathPrefix = strings.TrimSuffix(s.pathPrefix, "/")
		}
		jwksEndpoint = s.pathPrefix + JWKSEndpoint
		oidcEndpoint = s.pathPrefix + OIDCDiscoveryEndpoint
		log.Infof("Using path prefix '%s' for OIDC HTTP endpoints", s.pathPrefix)
	}

	// Add JWKS endpoint with domain validation wrapper
	mux.HandleFunc(jwksEndpoint, s.domainValidationHandler(s.handleJWKS))

	// Add OIDC discovery endpoint with domain validation wrapper
	mux.HandleFunc(oidcEndpoint, s.domainValidationHandler(s.handleOIDCDiscovery))

	addr := fmt.Sprintf("%s:%d", s.listenAddress, s.port)

	if len(s.domains) > 0 {
		log.Infof("OIDC server will only accept requests for domains: %v", s.domains)
	}

	s.server = &http.Server{
		Addr:      addr,
		Handler:   mux,
		TLSConfig: s.tlsConfig,
	}

	errCh := make(chan error, 1)
	go func() {
		if s.insecure {
			log.Infof("Starting OIDC HTTP server (insecure) on %s", addr)
			if err := s.server.ListenAndServe(); err != http.ErrServerClosed {
				errCh <- fmt.Errorf("OIDC HTTP server error: %w", err)
				return
			}
		} else {
			log.Infof("Starting OIDC HTTP server on %s", addr)
			if err := s.server.ListenAndServeTLS("", ""); err != http.ErrServerClosed {
				errCh <- fmt.Errorf("OIDC HTTP server error: %w", err)
				return
			}
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
		log.Info("Shutting down OIDC HTTP server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.server.Shutdown(shutdownCtx)
	}
}

// handleJWKS handles requests to the JWKS endpoint
func (s *Server) handleJWKS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.jwks == nil || len(s.jwks) == 0 {
		log.Error("JWKS not available in bundle")
		http.Error(w, "JWKS not available", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)

	// The JWKS is already a marshaled JSON object, so we can write it directly
	if _, err := w.Write(s.jwks); err != nil {
		log.Errorf("Failed to write JWKS response: %v", err)
	}
}

// OIDCDiscoveryDocument is a partial implementation of the OIDC discovery document
// it contains only the fields that are relevant for a resource provider to validate
// a JWT token issued by the Sentry server. We do not implement the full OIDC spec.
//
// reference: https://openid.net/specs/openid-connect-discovery-1_0.html
type OIDCDiscoveryDocument struct {
	// REQUIRED. URL using the https scheme with no query or fragment components that the OP asserts as its Issuer Identifier. If Issuer discovery is supported (see Section 2), this value MUST be identical to the issuer value returned by WebFinger. This also MUST be identical to the iss Claim value in ID Tokens issued from this Issuer.
	Issuer string `json:"issuer"`
	// REQUIRED. URL using the https scheme with no query or fragment components that the OP asserts as its Issuer Identifier. If Issuer discovery is supported (see Section 2), this value MUST be identical to the issuer value returned by WebFinger. This also MUST be identical to the iss Claim value in ID Tokens issued from this Issuer.
	AuthorizationEndpoint string `json:"authorization_endpoint,omitempty"`
	// REQUIRED. URL of the OP's JWK Set [JWK] document, which MUST use the https scheme. This contains the signing key(s) the RP uses to validate signatures from the OP. The JWK Set MAY also contain the Server's encryption key(s), which are used by RPs to encrypt requests to the Server. When both signing and encryption keys are made available, a use (public key use) parameter value is REQUIRED for all keys in the referenced JWK Set to indicate each key's intended usage. Although some algorithms allow the same key to be used for both signatures and encryption, doing so is NOT RECOMMENDED, as it is less secure. The JWK x5c parameter MAY be used to provide X.509 representations of keys provided. When used, the bare key values MUST still be present and MUST match those in the certificate. The JWK Set MUST NOT contain private or symmetric key values.
	JwksURI string `json:"jwks_uri"`
	// REQUIRED. JSON array containing a list of the OAuth 2.0 response_type values that this OP supports. Dynamic OpenID Providers MUST support the code, id_token, and the id_token token Response Type values.
	ResponseTypesSupported []string `json:"response_types_supported"`
	// REQUIRED. JSON array containing a list of the Subject Identifier types that this OP supports. Valid types include pairwise and public.
	SubjectTypesSupported []string `json:"subject_types_supported"`
	// REQUIRED. JSON array containing a list of the JWS signing algorithms (alg values) supported by the OP for the ID Token to encode the Claims in a JWT [JWT]. The algorithm RS256 MUST be included. The value none MAY be supported but MUST NOT be used unless the Response Type used returns no ID Token from the Authorization Endpoint (such as when using the Authorization Code Flow)
	IDTokenSigningAlgValuesSupported []string `json:"id_token_signing_alg_values_supported"`
	// RECOMMENDED. JSON array containing a list of the OAuth 2.0 [RFC6749] scope values that this server supports. The server MUST support the openid scope value. Servers MAY choose not to advertise some supported scope values even when this parameter is used, although those defined in [OpenID.Core] SHOULD be listed, if supported.
	ScopesSupported []string `json:"scopes_supported,omitempty"`
	// RECOMMENDED. JSON array containing a list of the Claim Names of the Claims that the OpenID Provider MAY be able to supply values for. Note that for privacy or other reasons, this might not be an exhaustive list.
	ClaimsSupported []string `json:"claims_supported,omitempty"`
}

// handleOIDCDiscovery handles requests to the OIDC discovery endpoint
func (s *Server) handleOIDCDiscovery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.jwks == nil || len(s.jwks) == 0 {
		log.Error("JWKS not available in bundle")
		http.Error(w, "OIDC configuration not available", http.StatusNotFound)
		return
	}

	// Use HTTP instead of HTTPS when in insecure mode
	scheme := "https"
	if s.insecure {
		scheme = "http"
		log.Debug("Using HTTP scheme for OIDC discovery document in insecure mode")
	}

	host := r.Host
	if host == "" {
		host = fmt.Sprintf("%s:%d", s.listenAddress, s.port)
	}

	var (
		issuerURL *url.URL
		err       error
	)
	if s.jwtIssuer == "" {
		issuerURL = &url.URL{
			Scheme: scheme,
			Host:   host,
		}
		if s.pathPrefix != "/" {
			issuerURL.Path = s.pathPrefix
		}
	} else {
		issuerURL, err = url.Parse(s.jwtIssuer)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if issuerURL.Scheme == "" {
			issuerURL.Scheme = scheme
		}
	}

	var jwksURI *url.URL
	switch {
	case s.jwksURI != "":
		// if the jwksURI is set, use that
		uri, err := url.Parse(s.jwksURI)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jwksURI = uri
		// If in insecure mode and no scheme is specified, use HTTP
		if s.insecure && jwksURI.Scheme == "" {
			jwksURI.Scheme = scheme
		}
	case s.jwtIssuer != "":
		// if the issuer is set, use that
		jwksURI, err = url.Parse(s.jwtIssuer)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if jwksURI.Scheme == "" {
			jwksURI.Scheme = scheme
		}
		if s.pathPrefix != "/" {
			jwksURI.Path = s.pathPrefix
		}
		jwksURI.Path += JWKSEndpoint
	default:
		// if nothing is set, use
		// the host from the request
		keysPath, err := url.JoinPath(s.pathPrefix, JWKSEndpoint)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jwksURI = &url.URL{
			Scheme: scheme,
			Host:   r.Host,
			Path:   keysPath,
		}
	}

	var issuer, jwks string
	if issuerURL != nil {
		issuer = issuerURL.String()
	}
	if jwksURI != nil {
		jwks = jwksURI.String()
	}

	// Create the OIDC discovery document that matches the JWT token implementation
	discovery := OIDCDiscoveryDocument{
		Issuer:                           issuer,
		JwksURI:                          jwks,
		AuthorizationEndpoint:            issuer + "/authorize", // WARN: not implemented
		ResponseTypesSupported:           []string{"id_token"},
		SubjectTypesSupported:            []string{"public"},
		IDTokenSigningAlgValuesSupported: []string{string(s.signatureAlgorithm.String())},
		ScopesSupported:                  []string{"openid"},
		ClaimsSupported:                  []string{"sub", "iss", "aud", "exp", "iat", "nbf", "use"},
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(discovery); err != nil {
		log.Errorf("Failed to write OIDC discovery response: %v", err)
	}
}

// domainValidationHandler wraps a handler with domain validation logic
func (s *Server) domainValidationHandler(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Skip domain validation if no domains are configured
		if len(s.domains) == 0 {
			h(w, r)
			return
		}

		// Check the Host header first
		host := r.Host

		// If X-Forwarded-Host header is present, use that instead
		if forwardedHost := r.Header.Get("X-Forwarded-Host"); forwardedHost != "" {
			host = forwardedHost
		}

		// Extract domain from host (remove port if present)
		domain := host
		if i := strings.Index(host, ":"); i > 0 {
			domain = host[:i]
		}

		// Check if the domain is in the allowed list
		allowed := false
		for _, allowedDomain := range s.domains {
			if allowedDomain == domain || allowedDomain == "*" {
				allowed = true
				break
			}
		}

		if !allowed {
			log.Warnf("Request from unauthorized domain: %s", domain)
			http.Error(w, "Forbidden: unauthorized domain", http.StatusForbidden)
			return
		}

		h(w, r)
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
