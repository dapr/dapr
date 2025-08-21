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
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(basicOIDCServer))
}

// basicOIDCServer tests OIDC resource server
type basicOIDCServer struct {
	sentry       *sentry.Sentry
	oidcProvider *oidc.Provider
	oauth2Config *oauth2.Config
	testBundle   *bundle.Bundle
	oidcBaseURL  string
	tlsCertFile  string
	tlsKeyFile   string
}

func (o *basicOIDCServer) Setup(t *testing.T) []framework.Option {
	// Create TLS certificate for OIDC server
	cert, key := o.generateTLSCertificate(t)
	tmpDir := t.TempDir()
	o.tlsCertFile = filepath.Join(tmpDir, "oidc.crt")
	o.tlsKeyFile = filepath.Join(tmpDir, "oidc.key")
	require.NoError(t, os.WriteFile(o.tlsCertFile, cert, 0o600))
	require.NoError(t, os.WriteFile(o.tlsKeyFile, key, 0o600))

	// Set up Sentry with JWT and OIDC enabled
	o.sentry = sentry.New(t,
		sentry.WithMode("standalone"),
		sentry.WithEnableJWT(true),
		sentry.WithJWTTTL(2*time.Hour),
		sentry.WithJWTIssuerFromOIDC(),
		sentry.WithOIDCEnabled(true),
		sentry.WithOIDCTLSCertFile(o.tlsCertFile),
		sentry.WithOIDCTLSKeyFile(o.tlsKeyFile),
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

func (o *basicOIDCServer) Run(t *testing.T, ctx context.Context) {
	o.sentry.WaitUntilRunning(t, ctx)

	// Set up OIDC provider with custom HTTP client that trusts our test certificate
	httpClient := o.createHTTPClientWithCustomCA(t)
	clientCtx := oidc.ClientContext(ctx, httpClient)

	// Initialize OIDC provider
	var err error
	o.oidcProvider, err = oidc.NewProvider(clientCtx, o.oidcBaseURL)
	require.NoError(t, err)

	// Set up OAuth2 configuration
	o.oauth2Config = &oauth2.Config{
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		Endpoint:     o.oidcProvider.Endpoint(),
		RedirectURL:  "http://localhost:8080/callback",
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}

	t.Run("OIDC Discovery", o.testOIDCDiscovery)
	t.Run("JWKS Endpoint", o.testJWKSEndpoint)
	t.Run("JWT Token Validation", o.testJWTTokenValidation)
	t.Run("Multiple Audience Validation", o.testMultipleAudienceValidation)
	t.Run("Token Expiration", o.testTokenExpiration)
	t.Run("Endpoint Routing", o.testEndpointRouting)
	t.Run("Service to Service Authentication", o.testServiceToServiceAuthentication)
}

// testOIDCDiscovery tests the OIDC discovery endpoint
func (o *basicOIDCServer) testOIDCDiscovery(t *testing.T) {
	client := o.createHTTPClientWithCustomCA(t)

	resp, err := client.Get(o.oidcBaseURL + "/.well-known/openid-configuration")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var discovery map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&discovery)
	require.NoError(t, err)

	// Verify required discovery fields
	assert.Equal(t, o.oidcBaseURL, discovery["issuer"])
	assert.Equal(t, o.oidcBaseURL+"/jwks.json", discovery["jwks_uri"])
	assert.Contains(t, discovery["response_types_supported"], "id_token")
	assert.Contains(t, discovery["subject_types_supported"], "public")
	assert.Contains(t, discovery["id_token_signing_alg_values_supported"], "RS256")
}

// testJWKSEndpoint tests the JWKS endpoint and validates the key structure
func (o *basicOIDCServer) testJWKSEndpoint(t *testing.T) {
	client := o.createHTTPClientWithCustomCA(t)
	resp, err := client.Get(o.oidcBaseURL + "/jwks.json")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var jwks struct {
		Keys []map[string]interface{} `json:"keys"`
	}
	err = json.NewDecoder(resp.Body).Decode(&jwks)
	require.NoError(t, err)

	assert.Len(t, jwks.Keys, 1)
	key := jwks.Keys[0]
	assert.NotEmpty(t, key["kid"], "Key ID should be present")
	assert.Equal(t, "RSA", key["kty"], "Key type should be RSA")
	assert.Equal(t, "sig", key["use"], "Key should be for signing")
	assert.NotEmpty(t, key["n"], "RSA modulus should be present")
	assert.NotEmpty(t, key["e"], "RSA exponent should be present")
}

// testJWTTokenValidation tests comprehensive JWT token validation with standard claims
func (o *basicOIDCServer) testJWTTokenValidation(t *testing.T) {
	conn := o.sentry.DialGRPC(t, t.Context(), "spiffe://localhost/ns/default/dapr-sentry")
	defer conn.Close()
	client := sentryv1pb.NewCAClient(conn)

	// Generate certificate request
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	csrTemplate := x509.CertificateRequest{Subject: pkix.Name{CommonName: "test-app"}}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &csrTemplate, privateKey)
	require.NoError(t, err)
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})

	// Request certificate with JWT token
	resp, err := client.SignCertificate(t.Context(), &sentryv1pb.SignCertificateRequest{
		Id:                        "test-app",
		Namespace:                 "default",
		TrustDomain:               "localhost",
		CertificateSigningRequest: csrPEM,
		TokenValidator:            sentryv1pb.SignCertificateRequest_INSECURE,
		JwtAudiences:              []string{"test-audience"},
	})
	require.NoError(t, err)
	require.NotNil(t, resp.GetJwt(), "Sentry should have issued a JWT token")

	tokenString := resp.GetJwt()
	require.NotEmpty(t, tokenString, "JWT token should not be empty")

	// Validate token using OIDC provider
	verifier := o.oidcProvider.Verifier(&oidc.Config{ClientID: "test-audience"})
	idToken, err := verifier.Verify(t.Context(), tokenString.GetValue())
	require.NoError(t, err, "Resource provider should be able to validate Sentry-issued JWT")

	// Verify standard claims
	assert.Equal(t, o.oidcBaseURL, idToken.Issuer, "Issuer should match Sentry's OIDC issuer")
	assert.Equal(t, "spiffe://localhost/ns/default/test-app", idToken.Subject, "Subject should be SPIFFE ID")
	assert.Contains(t, idToken.Audience, "test-audience", "Audience should include requested audience")
	assert.Contains(t, idToken.Audience, "localhost", "Audience should include trust domain as default")
	assert.True(t, idToken.Expiry.After(time.Now()), "Token should not be expired")

	// Verify custom claims
	var claims map[string]interface{}
	err = idToken.Claims(&claims)
	require.NoError(t, err)
	assert.NotEmpty(t, claims["iat"], "Issued at time should be present")
	assert.NotEmpty(t, claims["exp"], "Expiration time should be present")
	assert.NotEmpty(t, claims["jti"], "JWT ID should be present")

	// Verify trust domain audience validation
	trustDomainVerifier := o.oidcProvider.Verifier(&oidc.Config{ClientID: "localhost"})
	trustDomainToken, err := trustDomainVerifier.Verify(t.Context(), tokenString.GetValue())
	require.NoError(t, err, "Token should be valid for trust domain audience")
	assert.Contains(t, trustDomainToken.Audience, "localhost")
}

// testMultipleAudienceValidation tests token validation with multiple audiences and scenarios
func (o *basicOIDCServer) testMultipleAudienceValidation(t *testing.T) {
	conn := o.sentry.DialGRPC(t, t.Context(), "spiffe://localhost/ns/default/dapr-sentry")
	defer conn.Close()
	client := sentryv1pb.NewCAClient(conn)

	testCases := []struct {
		name      string
		audiences []string
		appID     string
		namespace string
	}{
		{"standard app", []string{"dapr-api"}, "myapp", "default"},
		{"multiple audiences", []string{"dapr-api", "placement", "scheduler"}, "operator", "system"},
		{"system service", []string{"long-term-service"}, "sentry", "system"},
	}

	for _, tc := range testCases {
		privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)
		csrTemplate := x509.CertificateRequest{Subject: pkix.Name{CommonName: tc.appID}}
		csrDER, err := x509.CreateCertificateRequest(rand.Reader, &csrTemplate, privateKey)
		require.NoError(t, err)
		csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})

		resp, err := client.SignCertificate(t.Context(), &sentryv1pb.SignCertificateRequest{
			Id:                        tc.appID,
			Namespace:                 tc.namespace,
			TrustDomain:               "localhost",
			CertificateSigningRequest: csrPEM,
			TokenValidator:            sentryv1pb.SignCertificateRequest_INSECURE,
			JwtAudiences:              tc.audiences,
		})
		require.NoError(t, err)
		require.NotNil(t, resp.GetJwt())

		tokenString := resp.GetJwt()
		expectedSubject := fmt.Sprintf("spiffe://localhost/ns/%s/%s", tc.namespace, tc.appID)

		// Test each requested audience
		for _, aud := range tc.audiences {
			verifier := o.oidcProvider.Verifier(&oidc.Config{ClientID: aud})
			idToken, verifyErr := verifier.Verify(t.Context(), tokenString.GetValue())
			require.NoError(t, verifyErr, "Token should be valid for audience %s in test case %s", aud, tc.name)
			assert.Equal(t, o.oidcBaseURL, idToken.Issuer)
			assert.Equal(t, expectedSubject, idToken.Subject)
			assert.Contains(t, idToken.Audience, aud)
			assert.True(t, idToken.Expiry.After(time.Now()))
		}

		// Test trust domain audience (always included)
		verifier := o.oidcProvider.Verifier(&oidc.Config{ClientID: "localhost"})
		idToken, verifyErr := verifier.Verify(t.Context(), tokenString.GetValue())
		require.NoError(t, verifyErr, "Token should be valid for trust domain audience in test case %s", tc.name)
		assert.Contains(t, idToken.Audience, "localhost")

		// Test invalid audience
		invalidVerifier := o.oidcProvider.Verifier(&oidc.Config{ClientID: "invalid-audience"})
		_, err = invalidVerifier.Verify(t.Context(), tokenString.GetValue())
		assert.Error(t, err, "Token should be invalid for unauthorized audience in test case %s", tc.name)
	}
}

// testTokenExpiration tests token expiration handling and TTL validation
func (o *basicOIDCServer) testTokenExpiration(t *testing.T) {
	conn := o.sentry.DialGRPC(t, t.Context(), "spiffe://localhost/ns/default/dapr-sentry")
	defer conn.Close()
	client := sentryv1pb.NewCAClient(conn)

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	csrTemplate := x509.CertificateRequest{Subject: pkix.Name{CommonName: "test-app"}}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &csrTemplate, privateKey)
	require.NoError(t, err)
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})

	resp, err := client.SignCertificate(t.Context(), &sentryv1pb.SignCertificateRequest{
		Id:                        "test-app",
		Namespace:                 "default",
		TrustDomain:               "localhost",
		CertificateSigningRequest: csrPEM,
		TokenValidator:            sentryv1pb.SignCertificateRequest_INSECURE,
		JwtAudiences:              []string{"test-audience"},
	})
	require.NoError(t, err)
	require.NotNil(t, resp.GetJwt())

	tokenString := resp.GetJwt()
	require.NotEmpty(t, tokenString)

	verifier := o.oidcProvider.Verifier(&oidc.Config{ClientID: "test-audience"})
	idToken, err := verifier.Verify(t.Context(), tokenString.GetValue())
	require.NoError(t, err, "Freshly issued token should be valid")

	// Verify token timing properties
	assert.True(t, idToken.Expiry.After(time.Now()), "Token should not be expired")
	assert.True(t, idToken.Expiry.Before(time.Now().Add(25*time.Hour)), "Token should have reasonable TTL (configured as 2h)")

	// Verify token is within configured TTL bounds (2 hours ±5 minutes for timing tolerance)
	expectedMinExpiry := time.Now().Add(1*time.Hour + 55*time.Minute)
	expectedMaxExpiry := time.Now().Add(2*time.Hour + 5*time.Minute)
	assert.True(t, idToken.Expiry.After(expectedMinExpiry), "Token expiry should be close to configured TTL")
	assert.True(t, idToken.Expiry.Before(expectedMaxExpiry), "Token expiry should not exceed configured TTL")
}

// testEndpointRouting tests OIDC endpoint routing and HTTP response handling
func (o *basicOIDCServer) testEndpointRouting(t *testing.T) {
	client := o.createHTTPClientWithCustomCA(t)

	endpoints := []struct {
		path         string
		expectedCode int
		description  string
	}{
		{"/.well-known/openid-configuration", http.StatusOK, "discovery endpoint"},
		{"/jwks.json", http.StatusOK, "JWKS endpoint"},
		{"/invalid-endpoint", http.StatusNotFound, "invalid endpoint"},
		{"/", http.StatusNotFound, "root path"},
		{"/.well-known/invalid", http.StatusNotFound, "invalid well-known endpoint"},
	}

	for _, endpoint := range endpoints {
		resp, err := client.Get(o.oidcBaseURL + endpoint.path)
		require.NoError(t, err, "Failed to request %s", endpoint.description)
		resp.Body.Close()
		assert.Equal(t, endpoint.expectedCode, resp.StatusCode,
			"Unexpected status code for %s", endpoint.description)
	}
}

// testServiceToServiceAuthentication tests federated identity scenarios for service-to-service auth
func (o *basicOIDCServer) testServiceToServiceAuthentication(t *testing.T) {
	conn := o.sentry.DialGRPC(t, t.Context(), "spiffe://localhost/ns/default/dapr-sentry")
	defer conn.Close()
	client := sentryv1pb.NewCAClient(conn)

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	csrTemplate := x509.CertificateRequest{Subject: pkix.Name{CommonName: "payment-service"}}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &csrTemplate, privateKey)
	require.NoError(t, err)
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})

	// Simulate federated identity where external services validate tokens
	resp, err := client.SignCertificate(t.Context(), &sentryv1pb.SignCertificateRequest{
		Id:                        "payment-service",
		Namespace:                 "production",
		TrustDomain:               "localhost",
		CertificateSigningRequest: csrPEM,
		TokenValidator:            sentryv1pb.SignCertificateRequest_INSECURE,
		JwtAudiences:              []string{"bank-api", "audit-service", "fraud-detection"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.GetWorkloadCertificate(), "Certificate should be issued")
	require.NotNil(t, resp.GetJwt(), "JWT token should be issued")

	jwtToken := resp.GetJwt()
	require.NotEmpty(t, jwtToken, "JWT token should not be empty")
	expectedSubject := "spiffe://localhost/ns/production/payment-service"

	// Test cross-service authentication scenarios
	services := []string{"bank-api", "audit-service", "fraud-detection"}
	for _, serviceAudience := range services {
		verifier := o.oidcProvider.Verifier(&oidc.Config{ClientID: serviceAudience})
		token, verifyErr := verifier.Verify(t.Context(), jwtToken.GetValue())
		require.NoError(t, verifyErr, "%s should be able to validate the token", serviceAudience)

		assert.Equal(t, expectedSubject, token.Subject)
		assert.Equal(t, o.oidcBaseURL, token.Issuer)
		assert.Contains(t, token.Audience, serviceAudience)
		assert.Contains(t, token.Audience, "localhost") // Trust domain always included
		assert.True(t, token.Expiry.After(time.Now()))
	}

	// Test that unauthorized services cannot validate the token
	unauthorizedVerifier := o.oidcProvider.Verifier(&oidc.Config{ClientID: "unauthorized-service"})
	_, err = unauthorizedVerifier.Verify(t.Context(), jwtToken.GetValue())
	assert.Error(t, err, "Unauthorized service should not be able to validate the token")
}

func (o *basicOIDCServer) generateTLSCertificate(t *testing.T) ([]byte, []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Dapr Test"},
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
		DNSNames:    []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	require.NoError(t, err)

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	keyBytes, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})

	return certPEM, keyPEM
}

func (o *basicOIDCServer) createHTTPClientWithCustomCA(t *testing.T) *http.Client {
	t.Helper()

	certPEM, err := os.ReadFile(o.tlsCertFile)
	require.NoError(t, err)

	caCertPool := x509.NewCertPool()
	ok := caCertPool.AppendCertsFromPEM(certPEM)
	require.True(t, ok)

	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    caCertPool,
				MinVersion: tls.VersionTLS12,
			},
		},
		Timeout: 30 * time.Second,
	}
}
