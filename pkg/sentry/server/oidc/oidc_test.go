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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/healthz"
)

// TestHandleJWKS tests the JWKS endpoint handler.
func TestHandleJWKS(t *testing.T) {
	// Create a sample JWKS
	jwks := []byte(`{"keys":[{"kty":"EC","use":"sig","kid":"test-key","crv":"P-256","x":"test-x","y":"test-y"}]}`)

	// Create server with the sample JWKS
	s := createTestServer(t, jwks, "", nil)

	t.Run("valid request returns JWKS", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, JWKSEndpoint, nil)
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		s.handleJWKS(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
		require.Equal(t, "application/json", rr.Header().Get("Content-Type"))
		require.Equal(t, "public, max-age=3600", rr.Header().Get("Cache-Control"))

		// Parse the response to ensure it's valid JWKS
		var jwksResp map[string]interface{}
		err = json.Unmarshal(rr.Body.Bytes(), &jwksResp)
		require.NoError(t, err)

		// Verify the response contains the expected keys
		keys, ok := jwksResp["keys"].([]interface{})
		require.True(t, ok)
		require.Equal(t, 1, len(keys))

		key := keys[0].(map[string]interface{})
		require.Equal(t, "EC", key["kty"])
		require.Equal(t, "sig", key["use"])
		require.Equal(t, "test-key", key["kid"])
	})

	t.Run("invalid method returns 405", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, JWKSEndpoint, nil)
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		s.handleJWKS(rr, req)

		require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	})

	t.Run("missing JWKS returns 404", func(t *testing.T) {
		// Create server with no JWKS
		s := createTestServer(t, nil, "", nil)

		req, err := http.NewRequest(http.MethodGet, JWKSEndpoint, nil)
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		s.handleJWKS(rr, req)

		require.Equal(t, http.StatusNotFound, rr.Code)
	})
}

// TestHandleOIDCDiscovery tests the OIDC discovery endpoint handler.
func TestHandleOIDCDiscovery(t *testing.T) {
	jwks := []byte(`{"keys":[{"kty":"EC","use":"sig","kid":"test-key","crv":"P-256","x":"test-x","y":"test-y"}]}`)

	t.Run("valid request returns discovery document", func(t *testing.T) {
		s := createTestServer(t, jwks, "", nil)

		req, err := http.NewRequest(http.MethodGet, OIDCDiscoveryEndpoint, nil)
		req.Host = "example.com:8443"
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		s.handleOIDCDiscovery(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
		require.Equal(t, "application/json", rr.Header().Get("Content-Type"))
		require.Equal(t, "public, max-age=3600", rr.Header().Get("Cache-Control"))

		// Parse the response to ensure it's valid discovery document
		var discovery OIDCDiscoveryDocument
		err = json.Unmarshal(rr.Body.Bytes(), &discovery)
		require.NoError(t, err)

		// Verify essential fields are present
		require.NotEmpty(t, discovery.Issuer)
		require.NotEmpty(t, discovery.JwksURI)
		require.Contains(t, discovery.ResponseTypesSupported, "id_token")
		require.Contains(t, discovery.SubjectTypesSupported, "public")

		// Check the structure is correct (host-based issuer)
		require.Equal(t, "https://example.com:8443", discovery.Issuer)

		// The JWKS URI should match the server's configured jwksURI
		require.Equal(t, s.jwksURI, discovery.JwksURI, "JWKS URI should match the configured URI in the server")
	})

	t.Run("with custom issuer", func(t *testing.T) {
		customIssuer := "https://auth.example.org"
		s := createTestServer(t, jwks, customIssuer, nil)

		req, err := http.NewRequest(http.MethodGet, OIDCDiscoveryEndpoint, nil)
		req.Host = "example.com:8443"
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		s.handleOIDCDiscovery(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)

		// Parse the response
		var discovery OIDCDiscoveryDocument
		err = json.Unmarshal(rr.Body.Bytes(), &discovery)
		require.NoError(t, err)

		// Check that the custom issuer is used
		require.Equal(t, customIssuer, discovery.Issuer)
	})

	t.Run("with custom JWKS URI", func(t *testing.T) {
		customJWKSURI := "https://keys.example.org/keys"
		s := createTestServer(t, jwks, "", nil)
		s.jwksURI = customJWKSURI

		req, err := http.NewRequest(http.MethodGet, OIDCDiscoveryEndpoint, nil)
		req.Host = "example.com:8443"
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		s.handleOIDCDiscovery(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)

		var discovery OIDCDiscoveryDocument
		err = json.Unmarshal(rr.Body.Bytes(), &discovery)
		require.NoError(t, err)

		// The custom URI should be used directly
		require.Equal(t, customJWKSURI, discovery.JwksURI)
	})

	t.Run("with path prefix", func(t *testing.T) {
		pathPrefix := "/auth"
		s := createTestServer(t, jwks, "", nil)
		s.pathPrefix = pathPrefix

		// Override the JWKS URI to include the host from the request and the path prefix
		// This simulates how the server would be configured in a real deployment
		s.jwksURI = "https://example.com:8443/auth/jwks.json"

		req, err := http.NewRequest(http.MethodGet, pathPrefix+OIDCDiscoveryEndpoint, nil)
		req.Host = "example.com:8443"
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		s.handleOIDCDiscovery(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)

		var discovery OIDCDiscoveryDocument
		err = json.Unmarshal(rr.Body.Bytes(), &discovery)
		require.NoError(t, err)

		// Path prefix should be reflected in the issuer
		expectedIssuer := "https://example.com:8443" + pathPrefix
		require.Equal(t, expectedIssuer, discovery.Issuer, "Issuer should include path prefix")

		// The JWKS URI should match the configured URI
		require.Equal(t, s.jwksURI, discovery.JwksURI, "JWKS URI should match the configured URI with path prefix")
	})

	t.Run("with path prefix and custom issuer", func(t *testing.T) {
		pathPrefix := "/auth"
		customIssuer := "https://auth.example.org"
		s := createTestServer(t, jwks, customIssuer, nil)
		s.pathPrefix = pathPrefix

		req, err := http.NewRequest(http.MethodGet, pathPrefix+OIDCDiscoveryEndpoint, nil)
		req.Host = "example.com:8443"
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		s.handleOIDCDiscovery(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)

		var discovery OIDCDiscoveryDocument
		err = json.Unmarshal(rr.Body.Bytes(), &discovery)
		require.NoError(t, err)

		// Custom issuer should take precedence over host-based issuer
		require.Equal(t, customIssuer, discovery.Issuer)

		// When custom issuer is used, the jwksURI still comes from the configured jwksURI
		require.Equal(t, s.jwksURI, discovery.JwksURI)
	})

	t.Run("invalid method returns 405", func(t *testing.T) {
		s := createTestServer(t, jwks, "", nil)

		req, err := http.NewRequest(http.MethodPost, OIDCDiscoveryEndpoint, nil)
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		s.handleOIDCDiscovery(rr, req)

		require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	})

	t.Run("missing JWKS returns 404", func(t *testing.T) {
		s := createTestServer(t, nil, "", nil)

		req, err := http.NewRequest(http.MethodGet, OIDCDiscoveryEndpoint, nil)
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		s.handleOIDCDiscovery(rr, req)

		require.Equal(t, http.StatusNotFound, rr.Code)
	})
}

// TestDomainValidationHandler tests the domain validation logic.
func TestDomainValidationHandler(t *testing.T) {
	jwks := []byte(`{"keys":[{"kty":"EC","use":"sig","kid":"test-key","crv":"P-256","x":"test-x","y":"test-y"}]}`)
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}

	t.Run("no domain restriction allows any domain", func(t *testing.T) {
		s := createTestServer(t, jwks, "", nil)

		req, err := http.NewRequest(http.MethodGet, "/", nil)
		req.Host = "any-domain.com"
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		s.domainValidationHandler(handler)(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
		require.Equal(t, "success", rr.Body.String())
	})

	t.Run("matching domain is allowed", func(t *testing.T) {
		s := createTestServer(t, jwks, "", []string{"allowed-domain.com"})

		req, err := http.NewRequest(http.MethodGet, "/", nil)
		req.Host = "allowed-domain.com"
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		s.domainValidationHandler(handler)(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
		require.Equal(t, "success", rr.Body.String())
	})

	t.Run("non-matching domain is forbidden", func(t *testing.T) {
		s := createTestServer(t, jwks, "", []string{"allowed-domain.com"})

		req, err := http.NewRequest(http.MethodGet, "/", nil)
		req.Host = "forbidden-domain.com"
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		s.domainValidationHandler(handler)(rr, req)

		require.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("domain with port is handled correctly", func(t *testing.T) {
		s := createTestServer(t, jwks, "", []string{"allowed-domain.com"})

		req, err := http.NewRequest(http.MethodGet, "/", nil)
		req.Host = "allowed-domain.com:8443"
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		s.domainValidationHandler(handler)(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
		require.Equal(t, "success", rr.Body.String())
	})

	t.Run("X-Forwarded-Host header is respected", func(t *testing.T) {
		s := createTestServer(t, jwks, "", []string{"forwarded-domain.com"})

		req, err := http.NewRequest(http.MethodGet, "/", nil)
		req.Host = "original-domain.com"
		req.Header.Set("X-Forwarded-Host", "forwarded-domain.com")
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		s.domainValidationHandler(handler)(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
		require.Equal(t, "success", rr.Body.String())
	})

	t.Run("wildcard domain allows any domain", func(t *testing.T) {
		s := createTestServer(t, jwks, "", []string{"*"})

		req, err := http.NewRequest(http.MethodGet, "/", nil)
		req.Host = "any-domain.com"
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		s.domainValidationHandler(handler)(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
		require.Equal(t, "success", rr.Body.String())
	})
}

// TestServer_Start tests the server startup and shutdown functionality.
func TestServer_Start(t *testing.T) {
	jwks := []byte(`{"keys":[{"kty":"EC","use":"sig","kid":"test-key","crv":"P-256","x":"test-x","y":"test-y"}]}`)

	// Create a test certificate for TLS
	cert, key := createTestCertificate(t)
	tlsCert, err := tls.X509KeyPair(cert, key)
	require.NoError(t, err)

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		MinVersion:   tls.VersionTLS12,
	}

	s := createTestServer(t, jwks, "", nil)
	s.tlsConfig = tlsConfig
	s.jwksURI = "https://example.com/jwks.json"

	// Start the server in a goroutine
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Start(ctx)
	}()

	// Wait a bit for the server to start and then cancel the context to shut it down
	time.Sleep(200 * time.Millisecond)
	cancel()

	// Check that the server stopped without errors
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Server did not shut down in time")
	}
}

// TestJWKSResponse tests the JWKSResponse utility function.
func TestJWKSResponse(t *testing.T) {
	jwks := []byte(`{"keys":[{"kty":"EC","use":"sig","kid":"test-key","crv":"P-256","x":"test-x","y":"test-y"}]}`)

	resp, err := JWKSResponse(jwks)
	require.NoError(t, err)

	require.Contains(t, resp, "keys")
	keys, ok := resp["keys"].([]interface{})
	require.True(t, ok)
	require.Equal(t, 1, len(keys))

	key := keys[0].(map[string]interface{})
	require.Equal(t, "EC", key["kty"])
	require.Equal(t, "sig", key["use"])
	require.Equal(t, "test-key", key["kid"])
}

// TestNew tests the creation of a new server with various options.
func TestNew(t *testing.T) {
	jwks := []byte(`{"keys":[{"kty":"EC","use":"sig","kid":"test-key","crv":"P-256","x":"test-x","y":"test-y"}]}`)

	t.Run("default options", func(t *testing.T) {
		s := New(Options{
			Port:               8443,
			ListenAddress:      "0.0.0.0",
			JWKS:               jwks,
			Healthz:            healthz.New(),
			JWKSURI:            "https://example.com/jwks.json",
			SignatureAlgorithm: jwa.ES256,
		})

		assert.Equal(t, 8443, s.port)
		assert.Equal(t, "0.0.0.0", s.listenAddress)
		assert.Equal(t, jwks, s.jwks)
		assert.Equal(t, "https://example.com/jwks.json", s.jwksURI)
		assert.Equal(t, "/", s.pathPrefix)
	})

	t.Run("with custom path prefix", func(t *testing.T) {
		s := New(Options{
			Port:               8443,
			ListenAddress:      "0.0.0.0",
			JWKS:               jwks,
			Healthz:            healthz.New(),
			JWKSURI:            "https://example.com/jwks.json",
			PathPrefix:         "/auth",
			SignatureAlgorithm: jwa.ES256,
		})

		assert.Equal(t, "/auth", s.pathPrefix)
	})

	t.Run("with custom domains", func(t *testing.T) {
		domains := []string{"example.com", "api.example.com"}
		s := New(Options{
			Port:               8443,
			ListenAddress:      "0.0.0.0",
			JWKS:               jwks,
			Healthz:            healthz.New(),
			JWKSURI:            "https://example.com/jwks.json",
			Domains:            domains,
			SignatureAlgorithm: jwa.ES256,
		})

		assert.Equal(t, domains, s.domains)
	})

	t.Run("with custom issuer", func(t *testing.T) {
		s := New(Options{
			Port:               8443,
			ListenAddress:      "0.0.0.0",
			JWKS:               jwks,
			Healthz:            healthz.New(),
			JWKSURI:            "https://example.com/jwks.json",
			JWTIssuer:          "https://auth.example.com",
			SignatureAlgorithm: jwa.ES256,
		})

		assert.Equal(t, "https://auth.example.com", s.jwtIssuer)
	})
}

// Helper function to create a test server for unit tests.
func createTestServer(t *testing.T, jwks []byte, jwtIssuer string, domains []string) *Server {
	return &Server{
		port:               8443,
		listenAddress:      "0.0.0.0",
		jwks:               jwks,
		htarget:            healthz.New().AddTarget(),
		jwksURI:            "https://example.com/jwks.json",
		domains:            domains,
		jwtIssuer:          jwtIssuer,
		pathPrefix:         "/",
		signatureAlgorithm: jwa.ES256,
	}
}

// Helper function to create a test TLS certificate.
func createTestCertificate(t *testing.T) ([]byte, []byte) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	// Create a self-signed certificate
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Test Organization"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	require.NoError(t, err)

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	privKeyBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	require.NoError(t, err)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privKeyBytes})

	return certPEM, keyPEM
}
