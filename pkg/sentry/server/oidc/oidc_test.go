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
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/kit/ptr"
)

const testHost = "example.com:8443"

// TestHandleJWKS tests the JWKS endpoint handler.
func TestHandleJWKS(t *testing.T) {
	// Create a sample JWKS
	jwks := []byte(`{"keys":[{"kty":"EC","use":"sig","kid":"test-key","crv":"P-256","x":"test-x","y":"test-y"}]}`)

	s := createTestServer(t, WithJWKS(jwks))

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
		require.Len(t, keys, 1)

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
		s := createTestServer(t)

		req, err := http.NewRequest(http.MethodGet, JWKSEndpoint, nil)
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		s.handleJWKS(rr, req)

		require.Equal(t, http.StatusNotFound, rr.Code)
	})
}

// TestHandleDiscovery tests the OIDC discovery endpoint handler.
func TestHandleDiscovery(t *testing.T) {
	jwks := []byte(`{"keys":[{"kty":"EC","use":"sig","kid":"test-key","crv":"P-256","x":"test-x","y":"test-y"}]}`)

	t.Run("valid request returns discovery document", func(t *testing.T) {
		s := createTestServer(t, WithJWKS(jwks))

		req, err := http.NewRequest(http.MethodGet, OIDCDiscoveryEndpoint, nil)
		req.Host = testHost
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		s.handleDiscovery(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
		require.Equal(t, "application/json", rr.Header().Get("Content-Type"))
		require.Equal(t, "public, max-age=3600", rr.Header().Get("Cache-Control"))

		// Parse the response to ensure it's valid discovery document
		var discovery discoveryDocument
		err = json.Unmarshal(rr.Body.Bytes(), &discovery)
		require.NoError(t, err)

		// Verify essential fields are present
		require.NotEmpty(t, discovery.Issuer)
		require.NotEmpty(t, discovery.JwksURI)
		require.Contains(t, discovery.ResponseTypesSupported, "id_token")
		require.Contains(t, discovery.SubjectTypesSupported, "public")

		// Check the structure is correct (host-based issuer)
		require.Equal(t, "http://"+testHost, discovery.Issuer)

		// The JWKS URI should be dynamically constructed from the host and endpoint
		expectedJWKSURI := "http://" + testHost + "/jwks.json"
		require.Equal(t, expectedJWKSURI, discovery.JwksURI, "JWKS URI should be dynamically constructed when not explicitly set")
	})

	t.Run("with custom issuer", func(t *testing.T) {
		customIssuer := "https://auth.example.org"
		s := createTestServer(t, WithJWKS(jwks), WithIssuer(customIssuer))

		req, err := http.NewRequest(http.MethodGet, OIDCDiscoveryEndpoint, nil)
		req.Host = testHost
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		s.handleDiscovery(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)

		// Parse the response
		var discovery discoveryDocument
		err = json.Unmarshal(rr.Body.Bytes(), &discovery)
		require.NoError(t, err)

		// Check that the custom issuer is used
		require.Equal(t, customIssuer, discovery.Issuer)
	})

	t.Run("with custom JWKS URI", func(t *testing.T) {
		customJWKSURI := "https://keys.example.org/keys"
		s := createTestServer(t, WithJWKS(jwks), WithJWKSURI(customJWKSURI))

		req, err := http.NewRequest(http.MethodGet, OIDCDiscoveryEndpoint, nil)
		req.Host = testHost
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		s.handleDiscovery(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)

		var discovery discoveryDocument
		err = json.Unmarshal(rr.Body.Bytes(), &discovery)
		require.NoError(t, err)

		// The custom URI should be used directly
		require.Equal(t, customJWKSURI, discovery.JwksURI)
	})

	t.Run("with path prefix", func(t *testing.T) {
		pathPrefix := "/auth"
		s := createTestServer(t, WithJWKS(jwks), WithPathPrefix(pathPrefix))

		req, err := http.NewRequest(http.MethodGet, pathPrefix+OIDCDiscoveryEndpoint, nil)
		req.Host = testHost
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		s.handleDiscovery(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)

		var discovery discoveryDocument
		err = json.Unmarshal(rr.Body.Bytes(), &discovery)
		require.NoError(t, err)

		// The JWKS URI should include the path prefix
		expectedJWKSURI := "http://" + testHost + "/auth/jwks.json"
		require.Equal(t, expectedJWKSURI, discovery.JwksURI, "JWKS URI should include path prefix")
	})

	t.Run("with path prefix and custom issuer", func(t *testing.T) {
		pathPrefix := "/auth"
		customIssuer := "https://auth.example.org"
		s := createTestServer(t, WithJWKS(jwks), WithPathPrefix(pathPrefix), WithIssuer(customIssuer))

		req, err := http.NewRequest(http.MethodGet, pathPrefix+OIDCDiscoveryEndpoint, nil)
		req.Host = testHost
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		s.handleDiscovery(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)

		var discovery discoveryDocument
		err = json.Unmarshal(rr.Body.Bytes(), &discovery)
		require.NoError(t, err)

		// Custom issuer should take precedence over host-based issuer
		require.Equal(t, customIssuer, discovery.Issuer)

		// When custom issuer is used, the jwksURI should be constructed using the issuer's base URL with path prefix
		expectedJWKSURI := "https://auth.example.org/auth/jwks.json"
		require.Equal(t, expectedJWKSURI, discovery.JwksURI)
	})

	t.Run("invalid method returns 405", func(t *testing.T) {
		s := createTestServer(t, WithJWKS(jwks))

		req, err := http.NewRequest(http.MethodPost, OIDCDiscoveryEndpoint, nil)
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		s.handleDiscovery(rr, req)

		require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	})

	t.Run("missing JWKS returns 404", func(t *testing.T) {
		s := createTestServer(t)

		req, err := http.NewRequest(http.MethodGet, OIDCDiscoveryEndpoint, nil)
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		s.handleDiscovery(rr, req)

		require.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("with tls config", func(t *testing.T) {
		// Create a test TLS certificate
		certFile, keyFile := createTestCertificate(t)

		s := createTestServer(t, WithJWKS(jwks), WithTLS(certFile, keyFile))

		req, err := http.NewRequest(http.MethodGet, OIDCDiscoveryEndpoint, nil)
		req.Host = testHost
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		s.handleDiscovery(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
		var discovery discoveryDocument
		err = json.Unmarshal(rr.Body.Bytes(), &discovery)
		require.NoError(t, err)
		require.Equal(t, "https://"+testHost, discovery.Issuer, "Issuer should use https scheme with TLS enabled")
	})
}

// TestAllowedHostsValidationHandler tests the allowed hosts validation logic.
func TestAllowedHostsValidationHandler(t *testing.T) {
	jwks := []byte(`{"keys":[{"kty":"EC","use":"sig","kid":"test-key","crv":"P-256","x":"test-x","y":"test-y"}]}`)
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}

	t.Run("no allowed hosts restriction allows any hostname", func(t *testing.T) {
		s := createTestServer(t, WithJWKS(jwks))

		req, err := http.NewRequest(http.MethodGet, "/", nil)
		req.Host = "any-domain.com"
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		s.allowedHostsValidationHandler(handler)(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
		require.Equal(t, "success", rr.Body.String())
	})

	t.Run("matching hostname is allowed", func(t *testing.T) {
		s := createTestServer(t, WithJWKS(jwks), WithAllowedHosts([]string{"allowed-domain.com"}))

		req, err := http.NewRequest(http.MethodGet, "/", nil)
		req.Host = "allowed-domain.com"
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		s.allowedHostsValidationHandler(handler)(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
		require.Equal(t, "success", rr.Body.String())
	})

	t.Run("non-matching hostname is forbidden", func(t *testing.T) {
		s := createTestServer(t, WithJWKS(jwks), WithAllowedHosts([]string{"allowed-domain.com"}))

		req, err := http.NewRequest(http.MethodGet, "/", nil)
		req.Host = "forbidden-domain.com"
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		s.allowedHostsValidationHandler(handler)(rr, req)

		require.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("hostname with port is handled correctly", func(t *testing.T) {
		s := createTestServer(t, WithJWKS(jwks), WithAllowedHosts([]string{"allowed-domain.com:8443"}))

		req, err := http.NewRequest(http.MethodGet, "/", nil)
		req.Host = "allowed-domain.com:8443"
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		s.allowedHostsValidationHandler(handler)(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
		require.Equal(t, "success", rr.Body.String())
	})

	t.Run("X-Forwarded-Host header is respected", func(t *testing.T) {
		s := createTestServer(t, WithJWKS(jwks), WithAllowedHosts([]string{"forwarded-domain.com"}))

		req, err := http.NewRequest(http.MethodGet, "/", nil)
		req.Host = "original-domain.com"
		req.Header.Set("X-Forwarded-Host", "forwarded-domain.com")
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		s.allowedHostsValidationHandler(handler)(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
		require.Equal(t, "success", rr.Body.String())
	})

	t.Run("wildcard hostname allows any hostname", func(t *testing.T) {
		s := createTestServer(t, WithJWKS(jwks), WithAllowedHosts([]string{"*"}))

		req, err := http.NewRequest(http.MethodGet, "/", nil)
		req.Host = "any-domain.com"
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		s.allowedHostsValidationHandler(handler)(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
		require.Equal(t, "success", rr.Body.String())
	})
}

// TestServer_Start tests the server startup and shutdown functionality.
func TestServer_Start(t *testing.T) {
	jwks := []byte(`{"keys":[{"kty":"EC","use":"sig","kid":"test-key","crv":"P-256","x":"test-x","y":"test-y"}]}`)

	// Create a test certificate for TLS
	certFile, keyFile := createTestCertificate(t)

	s := createTestServer(t, WithJWKS(jwks))
	s.tlsCertPath = ptr.Of(certFile)
	s.tlsKeyPath = ptr.Of(keyFile)
	s.jwksURI = ptr.Of("https://example.com/jwks.json")

	// Start the server in a goroutine
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Run(ctx)
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

	resp, err := jwksResponse(t, jwks)
	require.NoError(t, err)

	require.Contains(t, resp, "keys")
	keys, ok := resp["keys"].([]interface{})
	require.True(t, ok)
	require.Len(t, keys, 1)

	key := keys[0].(map[string]interface{})
	require.Equal(t, "EC", key["kty"])
	require.Equal(t, "sig", key["use"])
	require.Equal(t, "test-key", key["kid"])
}

// TestNew tests the creation of a new server with various options.
func TestNew(t *testing.T) {
	jwks := []byte(`{"keys":[{"kty":"EC","use":"sig","kid":"test-key","crv":"P-256","x":"test-x","y":"test-y"}]}`)

	t.Run("default options", func(t *testing.T) {
		s, err := New(Options{
			Port:               8443,
			ListenAddress:      "0.0.0.0",
			JWKS:               jwks,
			Healthz:            healthz.New(),
			JWKSURI:            ptr.Of("https://example.com/jwks.json"),
			SignatureAlgorithm: jwa.ES256,
		})
		require.NoError(t, err)

		assert.Equal(t, 8443, s.port)
		assert.Equal(t, "0.0.0.0", s.listenAddress)
		assert.Equal(t, jwks, s.jwks)
		assert.Equal(t, ptr.Of("https://example.com/jwks.json"), s.jwksURI)
		assert.Nil(t, s.pathPrefix)
	})

	t.Run("with custom path prefix", func(t *testing.T) {
		s, err := New(Options{
			Port:               8443,
			ListenAddress:      "0.0.0.0",
			JWKS:               jwks,
			Healthz:            healthz.New(),
			JWKSURI:            ptr.Of("https://example.com/jwks.json"),
			PathPrefix:         ptr.Of("/auth"),
			SignatureAlgorithm: jwa.ES256,
		})
		require.NoError(t, err)

		assert.Equal(t, ptr.Of("/auth"), s.pathPrefix)
	})

	t.Run("with custom domains", func(t *testing.T) {
		allowedHosts := []string{"example.com", "api.example.com"}
		s, err := New(Options{
			Port:               8443,
			ListenAddress:      "0.0.0.0",
			JWKS:               jwks,
			Healthz:            healthz.New(),
			JWKSURI:            ptr.Of("https://example.com/jwks.json"),
			AllowedHosts:       allowedHosts,
			SignatureAlgorithm: jwa.ES256,
		})
		require.NoError(t, err)

		assert.Equal(t, allowedHosts, s.allowedHosts)
	})

	t.Run("with custom issuer", func(t *testing.T) {
		s, err := New(Options{
			Port:               8443,
			ListenAddress:      "0.0.0.0",
			JWKS:               jwks,
			Healthz:            healthz.New(),
			JWKSURI:            ptr.Of("https://example.com/jwks.json"),
			JWTIssuer:          ptr.Of("https://auth.example.com"),
			SignatureAlgorithm: jwa.ES256,
		})
		require.NoError(t, err)

		assert.Equal(t, ptr.Of("https://auth.example.com"), s.jwtIssuer)
	})
}

// Helper function to create a test server for unit tests.
func createTestServer(t *testing.T, opts ...OptionsBuilder) *Server {
	o := NewOptions(t)
	for _, opt := range opts {
		opt(&o)
	}

	s, err := New(o)
	require.NoError(t, err)
	return s
}

// Helper function to create a test TLS certificate.
func createTestCertificate(t *testing.T) (string, string) {
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

	dir := t.TempDir()
	certFile, keyFile := dir+"/test_cert.pem", dir+"/test_key.pem"
	require.NoError(t, os.WriteFile(certFile, certPEM, 0o600))
	require.NoError(t, os.WriteFile(keyFile, keyPEM, 0o600))

	return certFile, keyFile
}

type OptionsBuilder func(*Options)

func WithJWKS(jwks []byte) OptionsBuilder {
	return func(opts *Options) {
		opts.JWKS = jwks
	}
}

func WithJWKSURI(jwksURI string) OptionsBuilder {
	return func(opts *Options) {
		opts.JWKSURI = ptr.Of(jwksURI)
	}
}

func WithPathPrefix(pathPrefix string) OptionsBuilder {
	return func(opts *Options) {
		opts.PathPrefix = ptr.Of(pathPrefix)
	}
}

func WithIssuer(issuer string) OptionsBuilder {
	return func(opts *Options) {
		opts.JWTIssuer = ptr.Of(issuer)
	}
}

func WithTLS(certFile, keyFile string) OptionsBuilder {
	return func(opts *Options) {
		opts.TLSCertPath = ptr.Of(certFile)
		opts.TLSKeyPath = ptr.Of(keyFile)
	}
}

func WithAllowedHosts(allowedHosts []string) OptionsBuilder {
	return func(opts *Options) {
		opts.AllowedHosts = allowedHosts
	}
}

// NewOptions returns a base set of options for the OIDC server.
func NewOptions(t *testing.T) Options {
	t.Helper()

	return Options{
		Port:               8443,
		ListenAddress:      "0.0.0.0",
		Healthz:            healthz.New(),
		SignatureAlgorithm: jwa.ES256,
	}
}

// jwksResponse is a test utility to parse the JWKS response for validation
func jwksResponse(t *testing.T, jwksBytes []byte) (map[string]interface{}, error) {
	t.Helper()

	var response map[string]interface{}
	if err := json.Unmarshal(jwksBytes, &response); err != nil {
		return nil, err
	}
	return response, nil
}
