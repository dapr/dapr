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
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"

	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
)

const (
	// JWKSEndpoint is the endpoint that serves the JWKS for JWT validation
	JWKSEndpoint = "/jwks.json"
	// OIDCDiscoveryEndpoint is the endpoint that serves the OIDC discovery document
	OIDCDiscoveryEndpoint = "/.well-known/openid-configuration"
	// AuthorizationEndpoint is the endpoint for OIDC authorization
	AuthorizationEndpoint = "/authorize"
)

var log = logger.NewLogger("dapr.sentry.server.oidc.http")

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
	JWKSURI *string

	// AllowedHosts is a list of allowed hosts that a client request will be valid for.
	AllowedHosts []string

	TLSCertPath *string
	TLSKeyPath  *string

	// JWTIssuer is the issuer to use for JWT tokens (if not set, issuer not set)
	JWTIssuer *string

	// PathPrefix is a prefix to add to all HTTP endpoints
	PathPrefix *string

	// SignatureAlgorithm is the signature algorithm to use for JWT tokens
	SignatureAlgorithm jwa.KeyAlgorithm
}

// Server is a HTTP server that partially implements the OIDC spec.
// Its purpose is only to support 3rd party resource providers
// being able to verify the JWT tokens issued by the Sentry server
// which may be used by the Dapr runtime to authenticate to components.
type Server struct {
	port              int
	listenAddress     string
	jwks              []byte
	htarget           healthz.Target
	jwksURI           *string
	allowedHosts      []string
	tlsCertPath       *string
	tlsKeyPath        *string
	jwtIssuer         *string
	pathPrefix        *string
	discovery         *discoveryDocument
	oidcEndpoint      string // OIDC endpoint for authorization
	jwksEndpoint      string // JWKS endpoint for public key retrieval
	authorizeEndpoint string // Authorization endpoint for OIDC (not implemented in this server)
}

// New creates a new HTTP server with the given options
func New(opts Options) (*Server, error) {
	if opts.SignatureAlgorithm == nil {
		return nil, errors.New("signature algorithm is required")
	}

	discovery := &discoveryDocument{
		ResponseTypesSupported:           []string{"id_token"},
		SubjectTypesSupported:            []string{"public"},
		IDTokenSigningAlgValuesSupported: []string{opts.SignatureAlgorithm.String()},
		ScopesSupported:                  []string{"openid"},
		ClaimsSupported:                  []string{"sub", "iss", "aud", "exp", "iat", "nbf", "use"},
	}

	// if the issuer is not set, we will construct it based on the request
	if opts.JWTIssuer != nil {
		issuer, err := url.Parse(*opts.JWTIssuer)
		if err != nil {
			return nil, fmt.Errorf("invalid JWT issuer URI: %w", err)
		}
		discovery.Issuer = issuer.String()
	}

	if opts.JWKSURI != nil {
		jwksURI, err := url.Parse(*opts.JWKSURI)
		if err != nil {
			return nil, fmt.Errorf("invalid JWKS URI: %w", err)
		}
		discovery.JwksURI = jwksURI.String()
	}

	jwksEndpoint := JWKSEndpoint
	oidcEndpoint := OIDCDiscoveryEndpoint
	authorizeEndpoint := AuthorizationEndpoint
	if opts.PathPrefix != nil && *opts.PathPrefix != "/" {
		if strings.HasSuffix(*opts.PathPrefix, "/") {
			opts.PathPrefix = ptr.Of(strings.TrimSuffix(*opts.PathPrefix, "/"))
		}

		var err error

		jwksEndpoint, err = url.JoinPath(*opts.PathPrefix, JWKSEndpoint)
		if err != nil {
			return nil, fmt.Errorf("failed to join path for JWKS endpoint: %w", err)
		}
		oidcEndpoint, err = url.JoinPath(*opts.PathPrefix, OIDCDiscoveryEndpoint)
		if err != nil {
			return nil, fmt.Errorf("failed to join path for OIDC discovery endpoint: %w", err)
		}
		authorizeEndpoint, err = url.JoinPath(*opts.PathPrefix, authorizeEndpoint)
		if err != nil {
			return nil, fmt.Errorf("failed to join path for authorization endpoint: %w", err)
		}

		log.Infof("Using path prefix %q for OIDC HTTP endpoints", opts.PathPrefix)
	}

	return &Server{
		port:              opts.Port,
		listenAddress:     opts.ListenAddress,
		jwks:              opts.JWKS,
		htarget:           opts.Healthz.AddTarget("oidc-server"),
		jwksURI:           opts.JWKSURI,
		allowedHosts:      opts.AllowedHosts,
		tlsCertPath:       opts.TLSCertPath,
		tlsKeyPath:        opts.TLSKeyPath,
		jwtIssuer:         opts.JWTIssuer,
		pathPrefix:        opts.PathPrefix,
		authorizeEndpoint: authorizeEndpoint,
		jwksEndpoint:      jwksEndpoint,
		oidcEndpoint:      oidcEndpoint,
		discovery:         discovery,
	}, nil
}

func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()

	// Add JWKS endpoint with host validation wrapper
	mux.HandleFunc(s.jwksEndpoint, s.allowedHostsValidationHandler(s.handleJWKS))

	// Add OIDC discovery endpoint with host validation wrapper
	mux.HandleFunc(s.oidcEndpoint, s.allowedHostsValidationHandler(s.handleDiscovery))

	addr := net.JoinHostPort(s.listenAddress, strconv.Itoa(s.port))

	if len(s.allowedHosts) > 0 {
		log.Infof("OIDC server will only accept requests for hosts: %v", s.allowedHosts)
	} else {
		log.Info("OIDC server will accept requests for any host")
	}

	var tlsConfig *tls.Config
	if s.tlsCertPath != nil && s.tlsKeyPath != nil {
		for {
			_, errC := os.Stat(*s.tlsCertPath)
			_, errK := os.Stat(*s.tlsKeyPath)
			if errC == nil && errK == nil {
				break
			}

			log.Warnf("Waiting for TLS certificate and key files to be available: %s, %s", s.tlsCertPath, s.tlsKeyPath)

			select {
			case <-time.After(5 * time.Second):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		cert, err := tls.LoadX509KeyPair(*s.tlsCertPath, *s.tlsKeyPath)
		if err != nil {
			return fmt.Errorf("failed to load TLS certificate and key: %w", err)
		}

		tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}

		log.Infof("Loaded TLS certificate from %s and key from %s", *s.tlsCertPath, *s.tlsKeyPath)
	}

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: 10 * time.Second,
	}

	return concurrency.NewRunnerManager(
		func(ctx context.Context) error {
			listener, err := net.Listen("tcp", addr)
			if err != nil {
				return fmt.Errorf("failed to listen on %s: %w", addr, err)
			}
			s.htarget.Ready()
			if tlsConfig == nil {
				log.Infof("Starting OIDC HTTP server (insecure) on %s", addr)
				if err := server.Serve(listener); err != http.ErrServerClosed {
					return fmt.Errorf("OIDC HTTP server error: %w", err)
				}
				return nil
			}
			log.Infof("Starting OIDC HTTP server on %s", addr)
			tlsListener := tls.NewListener(listener, tlsConfig)
			if err := server.Serve(tlsListener); err != http.ErrServerClosed {
				return fmt.Errorf("OIDC HTTP server error: %w", err)
			}
			return nil
		},
		func(ctx context.Context) error {
			<-ctx.Done()
			s.htarget.NotReady()
			log.Info("Shutting down OIDC HTTP server")
			sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return server.Shutdown(sctx)
		},
	).Run(ctx)
}

// handleJWKS handles requests to the JWKS endpoint
func (s *Server) handleJWKS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if len(s.jwks) == 0 {
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

// discoveryDocument is a partial implementation of the OIDC discovery document
// it contains only the fields that are relevant for a resource provider to validate
// a JWT token issued by the Sentry server. We do not implement the full OIDC spec.
//
// reference: https://openid.net/specs/openid-connect-discovery-1_0.html
type discoveryDocument struct {
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

func (d *discoveryDocument) DeepCopy() *discoveryDocument {
	if d == nil {
		return nil
	}
	return &discoveryDocument{
		Issuer:                           d.Issuer,
		AuthorizationEndpoint:            d.AuthorizationEndpoint,
		JwksURI:                          d.JwksURI,
		ResponseTypesSupported:           slices.Clone(d.ResponseTypesSupported),
		SubjectTypesSupported:            slices.Clone(d.SubjectTypesSupported),
		IDTokenSigningAlgValuesSupported: slices.Clone(d.IDTokenSigningAlgValuesSupported),
		ScopesSupported:                  slices.Clone(d.ScopesSupported),
		ClaimsSupported:                  slices.Clone(d.ClaimsSupported),
	}
}

// handleOIDCDiscovery handles requests to the OIDC discovery endpoint
func (s *Server) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if len(s.jwks) == 0 {
		log.Error("JWKS not available in bundle")
		http.Error(w, "OIDC configuration not available", http.StatusNotFound)
		return
	}

	if s.discovery == nil {
		log.Error("OIDC discovery document is not initialized")
		http.Error(w, "OIDC configuration not available", http.StatusInternalServerError)
		return
	}

	discovery := s.discovery
	if discovery.Issuer == "" || (s.jwksURI == nil && discovery.JwksURI == "") {
		// if we are going to edit the discovery document, make a copy
		discovery = s.discovery.DeepCopy()

		host := r.Host
		if forwardedHost := r.Header.Get("X-Forwarded-Host"); forwardedHost != "" {
			host = forwardedHost
		}

		scheme := r.URL.Scheme
		if scheme == "" {
			if s.tlsCertPath != nil && s.tlsKeyPath != nil {
				scheme = "https"
			} else {
				scheme = "http"
			}
		}

		u, err := url.Parse(fmt.Sprintf("%s://%s", scheme, host))
		if err != nil {
			log.Errorf("Failed to parse host '%s': %v", host, err)
			http.Error(w, "Invalid host", http.StatusBadRequest)
			return
		}
		if discovery.Issuer == "" {
			discovery.Issuer = u.String()
		}
		if s.jwksURI == nil {
			jwksURL, err := url.Parse(s.jwksEndpoint)
			if err != nil {
				log.Errorf("Failed to parse JWKS endpoint '%s': %v", s.jwksEndpoint, err)
				http.Error(w, "Invalid JWKS endpoint", http.StatusInternalServerError)
				return
			}
			baseURL := u
			if discovery.Issuer != "" {
				issuerURL, err := url.Parse(discovery.Issuer)
				if err == nil {
					baseURL = issuerURL
				}
			}
			discovery.JwksURI = baseURL.ResolveReference(jwksURL).String()
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(*discovery); err != nil {
		log.Errorf("Failed to write OIDC discovery response: %v", err)
	}
}

// allowedHostsValidationHandler wraps a handler with allowed host validation logic
func (s *Server) allowedHostsValidationHandler(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if len(s.allowedHosts) == 0 {
			next(w, r)
			return
		}

		host := r.Host

		// If X-Forwarded-Host header is present, use that instead
		if forwardedHost := r.Header.Get("X-Forwarded-Host"); forwardedHost != "" {
			host = forwardedHost
		}

		if slices.Contains(s.allowedHosts, "*") {
			next(w, r)
			return
		}

		if slices.Contains(s.allowedHosts, host) {
			next(w, r)
			return
		}

		if h, _, err := net.SplitHostPort(host); err == nil {
			if slices.Contains(s.allowedHosts, h) {
				next(w, r)
				return
			}
		}

		log.Warnf("Request from unauthorized domain: %s", host)
		http.Error(w, "Forbidden: unauthorized domain", http.StatusForbidden)
	}
}
