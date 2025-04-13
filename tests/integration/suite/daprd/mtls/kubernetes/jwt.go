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

package kubernetes

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
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	sentrypbv1 "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(jwtvalidation))
}

// jwtvalidation tests Kubernetes daprd with mTLS enabled and validates JWT tokens.
type jwtvalidation struct {
	daprd           *procdaprd.Daprd
	sentry          *sentry.Sentry
	placement       *placement.Placement
	operator        *operator.Operator
	scheduler       *scheduler.Scheduler
	trustAnchors    []byte
	oidcTLSCert     []byte
	oidcTLSKey      []byte
	oidcTLSCertFile string
	oidcTLSKeyFile  string
}

func (j *jwtvalidation) Setup(t *testing.T) []framework.Option {
	// Create TLS certificate for OIDC server
	cert, key := generateTLSCertificate(t)
	j.oidcTLSCert = cert
	j.oidcTLSKey = key

	// Save TLS certificate and key to files
	tmpDir := t.TempDir()
	j.oidcTLSCertFile = filepath.Join(tmpDir, "oidc.crt")
	j.oidcTLSKeyFile = filepath.Join(tmpDir, "oidc.key")
	require.NoError(t, os.WriteFile(j.oidcTLSCertFile, cert, 0o600))
	require.NoError(t, os.WriteFile(j.oidcTLSKeyFile, key, 0o600))

	// Create a JWT signing key
	signingKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	// Save the signing key to a file
	signingKeyFile := filepath.Join(tmpDir, "sign.key")
	require.NoError(t, os.WriteFile(signingKeyFile, signingKey.D.Bytes(), 0o600))

	// Configure Sentry with JWT and OIDC enabled
	j.sentry = sentry.New(t,
		sentry.WithEnableJWT(true),                    // Enable JWT token issuance
		sentry.WithOIDCHTTPPort(8443),                 // Enable OIDC HTTP server on port 8443
		sentry.WithOIDCTLSCertFile(j.oidcTLSCertFile), // Provide TLS cert for OIDC server
		sentry.WithOIDCTLSKeyFile(j.oidcTLSKeyFile),   // Provide TLS key for OIDC server
	)

	bundle := j.sentry.CABundle()
	j.trustAnchors = bundle.TrustAnchors

	// Control plane services always serves with mTLS in kubernetes mode.
	taFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(taFile, bundle.TrustAnchors, 0o600))

	j.placement = placement.New(t,
		placement.WithEnableTLS(true),
		placement.WithTrustAnchorsFile(taFile),
		placement.WithSentryAddress(j.sentry.Address()),
	)

	j.scheduler = scheduler.New(t,
		scheduler.WithSentry(j.sentry),
		scheduler.WithID("dapr-scheduler-server-0"),
	)

	j.operator = operator.New(t, operator.WithSentry(j.sentry))

	j.daprd = procdaprd.New(t,
		procdaprd.WithAppID("my-app"),
		procdaprd.WithMode("kubernetes"),
		procdaprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(bundle.TrustAnchors))),
		procdaprd.WithSentryAddress(j.sentry.Address()),
		procdaprd.WithControlPlaneAddress(j.operator.Address(t)),
		procdaprd.WithDisableK8sSecretStore(true),
		procdaprd.WithPlacementAddresses(j.placement.Address()),
		procdaprd.WithSchedulerAddresses(j.scheduler.Address()),

		// Enable mTLS
		procdaprd.WithEnableMTLS(true),
		procdaprd.WithNamespace("default"),
	)

	return []framework.Option{
		framework.WithProcesses(j.sentry, j.placement, j.operator, j.scheduler, j.daprd),
	}
}

// generateTLSCertificate generates a TLS certificate and key pair for the OIDC server
func generateTLSCertificate(t *testing.T) ([]byte, []byte) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		Subject: pkix.Name{
			CommonName: "localhost",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	require.NoError(t, err)

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	keyBytes, err := x509.MarshalECPrivateKey(privateKey)
	require.NoError(t, err)

	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})

	return certPEM, keyPEM
}

// oidcDiscoveryResponse represents the OIDC discovery document
type oidcDiscoveryResponse struct {
	Issuer                           string   `json:"issuer"`
	JWKSURI                          string   `json:"jwks_uri"`
	ResponseTypesSupported           []string `json:"response_types_supported"`
	SubjectTypesSupported            []string `json:"subject_types_supported"`
	IDTokenSigningAlgValuesSupported []string `json:"id_token_signing_alg_values_supported"`
}

// generateCSR generates a CSR for testing with the given SPIFFE ID
func generateCSR(spiffeID string) ([]byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	spiffeURI, err := url.Parse(spiffeID)
	if err != nil {
		return nil, err
	}

	template := &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName: "test-subject",
		},
		URIs: []*url.URL{spiffeURI},
	}

	csrDER, err := x509.CreateCertificateRequest(rand.Reader, template, key)
	if err != nil {
		return nil, err
	}

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER}), nil
}

func (j *jwtvalidation) Run(t *testing.T, ctx context.Context) {
	j.sentry.WaitUntilRunning(t, ctx)
	j.placement.WaitUntilRunning(t, ctx)
	j.scheduler.WaitUntilRunning(t, ctx)
	j.daprd.WaitUntilRunning(t, ctx)

	t.Run("validate JWT token issued by Sentry service", func(t *testing.T) {
		// Setup a direct connection to Sentry
		conn, err := grpc.Dial(j.sentry.Address(), grpc.WithTransportCredentials(insecure.NewCredentials()))
		require.NoError(t, err)
		defer conn.Close()

		client := sentrypbv1.NewCAClient(conn)

		// Generate a CSR for testing
		spiffeID := "spiffe://integration.test.dapr.io/ns/default/test-app"
		csr, err := generateCSR(spiffeID)
		require.NoError(t, err)

		// Call SignCertificate and get a JWT token
		resp, err := client.SignCertificate(ctx, &sentrypbv1.SignCertificateRequest{
			CertificateSigningRequest: csr,
			TokenValidator:            sentrypbv1.SignCertificateRequest_INSECURE,
			Token:                     `{"kubernetes.io":{"pod":{"name":"test-pod"}}}`,
		})
		require.NoError(t, err)
		require.NotEmpty(t, resp.GetJwt(), "JWT token should be included in the response")

		// Create a custom HTTP client that skips certificate validation
		// since we're using a self-signed certificate for the OIDC server
		customTransport := &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		}
		httpClient := &http.Client{
			Transport: customTransport,
		}

		// Get OIDC discovery document from Sentry's OIDC HTTP server
		oidcURL := fmt.Sprintf("https://localhost:8443/.well-known/openid-configuration")
		httpResp, err := httpClient.Get(oidcURL)
		require.NoError(t, err)
		defer httpResp.Body.Close()

		require.Equal(t, http.StatusOK, httpResp.StatusCode, "OIDC discovery endpoint should be accessible")

		var discovery oidcDiscoveryResponse
		err = json.NewDecoder(httpResp.Body).Decode(&discovery)
		require.NoError(t, err)
		require.NotEmpty(t, discovery.JWKSURI, "JWKS URI should be present in discovery document")

		// Get JWKS from the URL provided in the discovery document
		jwksResp, err := httpClient.Get(discovery.JWKSURI)
		require.NoError(t, err)
		defer jwksResp.Body.Close()

		require.Equal(t, http.StatusOK, jwksResp.StatusCode, "JWKS endpoint should be accessible")

		// Parse JWKS
		keySet, err := jwk.ParseReader(jwksResp.Body)
		require.NoError(t, err)
		require.True(t, keySet.Len() > 0, "JWKS should contain at least one key")

		// Parse and validate the JWT token using the JWKS
		token, err := jwt.Parse([]byte(resp.GetJwt()), jwt.WithKeySet(keySet))
		require.NoError(t, err, "JWT token should be valid and verifiable with JWKS")

		// Validate expected claims
		issuer, _ := token.Get("iss")
		require.Equal(t, discovery.Issuer, issuer, "Issuer in JWT should match discovery document")

		sub, _ := token.Get("sub")
		require.Equal(t, spiffeID, sub, "Subject should match CSR SPIFFE ID")

		// Verify token expiration
		expTime, _ := token.Get("exp")
		require.NotNil(t, expTime, "Expiration claim should exist")

		// Verify token issuance time
		iatTime, _ := token.Get("iat")
		require.NotNil(t, iatTime, "Issued at claim should exist")

		// Verify token audience
		aud, ok := token.Get("aud")
		require.True(t, ok, "Audience claim should exist")
		require.NotEmpty(t, aud, "Audience claim should not be empty")

		// Validate that expiration is in the future
		exp, ok := expTime.(time.Time)
		require.True(t, ok, "exp claim should be a time value")
		require.True(t, exp.After(time.Now()), "Token should not be expired")

		// Validate that issued at is in the past
		iat, ok := iatTime.(time.Time)
		require.True(t, ok, "iat claim should be a time value")
		require.True(t, iat.Before(time.Now()) || iat.Equal(time.Now()), "Token should have been issued in the past or now")

		// Additional validation: validate token lifetime
		tokenLifetime := exp.Sub(iat)
		require.True(t, tokenLifetime > 0, "Token lifetime should be positive")
		t.Logf("Token lifetime: %v", tokenLifetime)
	})
}
