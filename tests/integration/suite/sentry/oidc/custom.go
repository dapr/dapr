// Copyright 2024 The Dapr Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//     http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package oidc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(customOIDCServer))
}

// customOIDCServer tests OIDC server customization features
type customOIDCServer struct {
	sentry        *sentry.Sentry
	testBundle    *bundle.Bundle
	oidcBaseURL   string
	tlsCertFile   string
	tlsKeyFile    string
	oidcTLSCert   []byte
	httpClient    *http.Client
	customJWKSURI string
	pathPrefix    string
	allowedHosts  []string
}

func (o *customOIDCServer) Setup(t *testing.T) []framework.Option {
	// Create TLS certificate for OIDC server
	cert, key := o.generateTLSCertificate(t)
	o.oidcTLSCert = cert
	tmpDir := t.TempDir()
	o.tlsCertFile = filepath.Join(tmpDir, "oidc.crt")
	o.tlsKeyFile = filepath.Join(tmpDir, "oidc.key")
	require.NoError(t, os.WriteFile(o.tlsCertFile, cert, 0o600))
	require.NoError(t, os.WriteFile(o.tlsKeyFile, key, 0o600))

	// Set test configuration
	o.customJWKSURI = "https://external.example.com/jwks"
	o.pathPrefix = "/auth"
	o.allowedHosts = []string{"localhost", "example.com", "api.example.com"}

	// Set up Sentry with JWT, OIDC enabled and customization options
	o.sentry = sentry.New(t,
		sentry.WithMode("standalone"),
		sentry.WithEnableJWT(true),
		sentry.WithJWTTTL(2*time.Hour),
		sentry.WithJWTIssuerFromOIDC(),
		sentry.WithOIDCEnabled(true),
		sentry.WithOIDCTLSCertFile(o.tlsCertFile),
		sentry.WithOIDCTLSKeyFile(o.tlsKeyFile),
		sentry.WithOIDCJWKSURI(o.customJWKSURI),     // Custom external JWKS URI
		sentry.WithOIDCPathPrefix(o.pathPrefix),     // Custom path prefix
		sentry.WithOIDCAllowedHosts(o.allowedHosts), // Allowed hosts restriction
	)

	oidcPort := o.sentry.OIDCPort(t)
	require.NotNil(t, oidcPort, "OIDC port should not be nil when OIDC is enabled")

	testBundle := o.sentry.CABundle()
	o.testBundle = &testBundle
	o.oidcBaseURL = fmt.Sprintf("https://localhost:%d", oidcPort)

	return []framework.Option{
		framework.WithProcesses(o.sentry),
	}
}

func (o *customOIDCServer) Run(t *testing.T, ctx context.Context) {
	o.sentry.WaitUntilRunning(t, ctx)

	// Create HTTP client that trusts our test certificate
	o.httpClient = o.createHTTPClientWithCustomCA(t)

	t.Run("Path Prefix Configuration", o.testPathPrefixConfiguration)
	t.Run("Custom JWKS URI Override", o.testCustomJWKSURI)
	t.Run("Allowed Hosts Validation", o.testAllowedHostsValidation)
	t.Run("X-Forwarded-Host Header Support", o.testXForwardedHostSupport)
	t.Run("HTTP Method Validation", o.testHTTPMethodValidation)
	t.Run("HTTPS Scheme Detection", o.testHTTPSSchemeDetection)
	t.Run("Insecure HTTP Mode", o.testInsecureHTTPMode)
}

// testPathPrefixConfiguration tests that endpoints are accessible with path prefix
func (o *customOIDCServer) testPathPrefixConfiguration(t *testing.T) {
	// Test discovery endpoint with path prefix
	discoveryURL := o.oidcBaseURL + o.pathPrefix + "/.well-known/openid-configuration"
	resp, err := o.httpClient.Get(discoveryURL)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var discovery map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&discovery)
	require.NoError(t, err)

	// Verify issuer includes path prefix
	expectedIssuer := fmt.Sprintf("https://localhost:%d%s", o.sentry.OIDCPort(t), o.pathPrefix)
	assert.Equal(t, expectedIssuer, discovery["issuer"])

	// Test JWKS endpoint with path prefix
	jwksURL := o.oidcBaseURL + o.pathPrefix + "/jwks.json"
	resp, err = o.httpClient.Get(jwksURL)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	// Test that endpoints without path prefix return 404
	resp, err = o.httpClient.Get(o.oidcBaseURL + "/.well-known/openid-configuration")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// testCustomJWKSURI tests that custom JWKS URI is used in discovery document
func (o *customOIDCServer) testCustomJWKSURI(t *testing.T) {
	discoveryURL := o.oidcBaseURL + o.pathPrefix + "/.well-known/openid-configuration"
	resp, err := o.httpClient.Get(discoveryURL)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var discovery map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&discovery)
	require.NoError(t, err)

	// Verify custom JWKS URI is used instead of default server-generated one
	assert.Equal(t, o.customJWKSURI, discovery["jwks_uri"])
	assert.NotContains(t, discovery["jwks_uri"], "localhost", "Should use custom external URI")
}

// testAllowedHostsValidation tests host validation logic
func (o *customOIDCServer) testAllowedHostsValidation(t *testing.T) {
	// Create custom client with different Host headers
	client := o.createHTTPClientWithCustomCA(t)

	// Test allowed host
	req, err := http.NewRequest(http.MethodGet, o.oidcBaseURL+o.pathPrefix+"/.well-known/openid-configuration", nil)
	require.NoError(t, err)
	req.Host = "localhost"
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "Allowed host should be accepted")

	// Test another allowed host
	req, err = http.NewRequest(http.MethodGet, o.oidcBaseURL+o.pathPrefix+"/.well-known/openid-configuration", nil)
	require.NoError(t, err)
	req.Host = "example.com"
	resp, err = client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "Allowed host should be accepted")

	// Test forbidden host
	req, err = http.NewRequest(http.MethodGet, o.oidcBaseURL+o.pathPrefix+"/.well-known/openid-configuration", nil)
	require.NoError(t, err)
	req.Host = "forbidden.com"
	resp, err = client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "Non-allowed host should be forbidden")

	// Test host with port is handled correctly
	req, err = http.NewRequest(http.MethodGet, o.oidcBaseURL+o.pathPrefix+"/.well-known/openid-configuration", nil)
	require.NoError(t, err)
	req.Host = "api.example.com:8443"
	resp, err = client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "Allowed host with port should be accepted")
}

// testXForwardedHostSupport tests X-Forwarded-Host header handling
func (o *customOIDCServer) testXForwardedHostSupport(t *testing.T) {
	client := o.createHTTPClientWithCustomCA(t)

	// Test X-Forwarded-Host header with allowed host
	req, err := http.NewRequest(http.MethodGet, o.oidcBaseURL+o.pathPrefix+"/.well-known/openid-configuration", nil)
	require.NoError(t, err)
	req.Host = "forbidden.com"                        // This would normally be forbidden
	req.Header.Set("X-Forwarded-Host", "example.com") // But X-Forwarded-Host is allowed
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "X-Forwarded-Host should take precedence over Host header")

	// Test X-Forwarded-Host header with forbidden host
	req, err = http.NewRequest(http.MethodGet, o.oidcBaseURL+o.pathPrefix+"/.well-known/openid-configuration", nil)
	require.NoError(t, err)
	req.Host = "localhost"                              // This would normally be allowed
	req.Header.Set("X-Forwarded-Host", "forbidden.com") // But X-Forwarded-Host is forbidden
	resp, err = client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "X-Forwarded-Host should be validated")
}

// testHTTPMethodValidation tests that only GET method is allowed
func (o *customOIDCServer) testHTTPMethodValidation(t *testing.T) {
	client := o.createHTTPClientWithCustomCA(t)
	endpoints := []string{
		o.oidcBaseURL + o.pathPrefix + "/.well-known/openid-configuration",
		o.oidcBaseURL + o.pathPrefix + "/jwks.json",
	}

	for _, endpoint := range endpoints {
		// Test invalid HTTP methods
		for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
			req, err := http.NewRequest(method, endpoint, nil)
			require.NoError(t, err)
			resp, err := client.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode,
				"Method %s should not be allowed on %s", method, endpoint)
		}

		// Test that GET method works
		resp, err := client.Get(endpoint)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode, "GET method should be allowed")
	}
}

// testHTTPSSchemeDetection tests that HTTPS scheme is properly detected with TLS
func (o *customOIDCServer) testHTTPSSchemeDetection(t *testing.T) {
	discoveryURL := o.oidcBaseURL + o.pathPrefix + "/.well-known/openid-configuration"
	resp, err := o.httpClient.Get(discoveryURL)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var discovery map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&discovery)
	require.NoError(t, err)

	// Verify issuer uses HTTPS scheme when TLS is enabled
	issuer := discovery["issuer"].(string)
	assert.Contains(t, issuer, "https://", "Issuer should use HTTPS scheme when TLS is enabled")
}

// testInsecureHTTPMode tests OIDC server in insecure HTTP mode
func (o *customOIDCServer) testInsecureHTTPMode(t *testing.T) {
	// Create a separate Sentry instance with insecure HTTP mode
	httpSentry := sentry.New(t,
		sentry.WithMode("standalone"),
		sentry.WithEnableJWT(true),
		sentry.WithJWTIssuerFromOIDC(),
		sentry.WithJWTTTL(2*time.Hour),
		sentry.WithOIDCEnabled(true),
	)

	// Start the HTTP sentry in the background
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	httpSentry.Run(t, ctx)
	t.Cleanup(func() {
		httpSentry.Cleanup(t)
	})

	httpSentry.WaitUntilRunning(t, ctx)

	httpOIDCPort := httpSentry.OIDCPort(t)
	require.NotNil(t, httpOIDCPort, "HTTP OIDC port should not be nil when OIDC is enabled")

	httpBaseURL := fmt.Sprintf("http://localhost:%d", httpOIDCPort)

	// Create regular HTTP client (no TLS)
	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	// Test discovery endpoint over HTTP
	resp, err := httpClient.Get(httpBaseURL + "/.well-known/openid-configuration")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var discovery map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&discovery)
	require.NoError(t, err)

	// Verify issuer uses HTTP scheme when TLS is disabled
	issuer := discovery["issuer"].(string)
	assert.Contains(t, issuer, "http://", "Issuer should use HTTP scheme when TLS is disabled")

	// Test JWKS endpoint over HTTP
	resp, err = httpClient.Get(httpBaseURL + "/jwks.json")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	// Cleanup
	httpSentry.Cleanup(t)
}

// generateTLSCertificate generates a TLS certificate and key pair for testing
func (o *customOIDCServer) generateTLSCertificate(t *testing.T) ([]byte, []byte) {
	// Generate private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	// Create certificate template
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization:  []string{"Dapr"},
			Country:       []string{"US"},
			Province:      []string{""},
			Locality:      []string{"San Francisco"},
			StreetAddress: []string{""},
			PostalCode:    []string{""},
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		DNSNames:    []string{"localhost"},
	}

	// Create certificate
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	require.NoError(t, err)

	// Encode certificate and private key to PEM format
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	require.NoError(t, err)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER})

	return certPEM, keyPEM
}

// createHTTPClientWithCustomCA creates an HTTP client that trusts our test certificate
func (o *customOIDCServer) createHTTPClientWithCustomCA(t *testing.T) *http.Client {
	// Create custom TLS config that trusts both the sentry CA and our OIDC server certificate
	rootCAs := x509.NewCertPool()

	// Add the sentry's CA bundle
	rootCAs.AppendCertsFromPEM(o.testBundle.X509.TrustAnchors)

	// Add the OIDC server's self-signed certificate
	rootCAs.AppendCertsFromPEM(o.oidcTLSCert)

	tlsConfig := &tls.Config{
		RootCAs:            rootCAs,
		InsecureSkipVerify: false,
		MinVersion:         tls.VersionTLS12,
	}

	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
		Timeout: 10 * time.Second,
	}
}
