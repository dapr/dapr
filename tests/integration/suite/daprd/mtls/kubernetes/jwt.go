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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	spiffecontext "github.com/dapr/kit/crypto/spiffe/context"
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

	// Configure Sentry with JWT and OIDC server enabled
	j.sentry = sentry.New(t,
		sentry.WithEnableJWT(true),                    // Enable JWT token issuance
		sentry.WithOIDCEnabled(true),                  // Enable OIDC server
		sentry.WithJWTIssuerFromOIDC(),                // Use OIDC for JWT issuer
		sentry.WithOIDCTLSCertFile(j.oidcTLSCertFile), // Provide TLS cert for OIDC server
		sentry.WithOIDCTLSKeyFile(j.oidcTLSKeyFile),   // Provide TLS key for OIDC server
	)

	bundle := j.sentry.CABundle()
	j.trustAnchors = bundle.X509.TrustAnchors

	// Control plane services always serves with mTLS in kubernetes mode.
	taFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(taFile, bundle.X509.TrustAnchors, 0o600))

	j.scheduler = scheduler.New(t,
		scheduler.WithSentry(j.sentry),
		scheduler.WithID("dapr-scheduler-server-0"),
	)

	j.placement = placement.New(t,
		placement.WithEnableTLS(true),
		placement.WithTrustAnchorsFile(taFile),
		placement.WithSentryAddress(j.sentry.Address()),
	)

	j.operator = operator.New(t, operator.WithSentry(j.sentry))

	j.daprd = procdaprd.New(t,
		procdaprd.WithAppID("kubernetes-app"),
		procdaprd.WithMode("kubernetes"),
		procdaprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(bundle.X509.TrustAnchors))),
		procdaprd.WithSentryAddress(j.sentry.Address()),
		procdaprd.WithControlPlaneAddress(j.operator.Address(t)),
		procdaprd.WithDisableK8sSecretStore(true),
		procdaprd.WithPlacementAddresses(j.placement.Address()),
		procdaprd.WithSchedulerAddresses(j.scheduler.Address()),
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

func (j *jwtvalidation) Run(t *testing.T, ctx context.Context) {
	j.sentry.WaitUntilRunning(t, ctx)
	j.placement.WaitUntilRunning(t, ctx)
	j.scheduler.WaitUntilRunning(t, ctx)
	j.daprd.WaitUntilRunning(t, ctx)

	sctx, cancel := context.WithCancel(ctx)

	secProv, err := security.New(sctx, security.Options{
		SentryAddress:           j.sentry.Address(),
		ControlPlaneTrustDomain: "localhost",
		ControlPlaneNamespace:   "default",
		TrustAnchors:            j.trustAnchors,
		AppID:                   "my-app",
		MTLSEnabled:             true,
		Healthz:                 healthz.New(),
	})
	require.NoError(t, err)

	secProvErr := make(chan error)
	go func() {
		secProvErr <- secProv.Run(sctx)
	}()

	t.Cleanup(func() {
		cancel()
		select {
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for security provider to stop")
		case err = <-secProvErr:
			require.NoError(t, err)
		}
	})

	sec, err := secProv.Handler(sctx)
	require.NoError(t, err)

	ctxWithIdentity := sec.WithSVIDContext(ctx)
	jwtSource, ok := spiffecontext.JWTFrom(ctxWithIdentity)
	require.True(t, ok, "JWT source should be present in context")
	require.NotNil(t, jwtSource, "JWT source should not be nil")

	myAppID, err := spiffeid.FromSegments(spiffeid.RequireTrustDomainFromString("public"), "ns", "default", "my-app")
	require.NoError(t, err)

	token, err := jwtSource.FetchJWTSVID(ctx, jwtsvid.Params{
		Audience: "public",
		Subject:  myAppID,
	})
	require.NoError(t, err, "JWT token should be fetched successfully")
	require.NotEmpty(t, token, "JWT token should not be empty")

	tokenRaw := token.Marshal()

	// Create a custom HTTP client that skips certificate validation
	// since we're using a self-signed certificate for the OIDC server
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, //nolint: gosec
			},
		},
	}

	// Get OIDC discovery document from Sentry's OIDC HTTP server
	oidcURL := fmt.Sprintf("https://localhost:%d/.well-known/openid-configuration", j.sentry.OIDCPort(t))
	httpResp, err := httpClient.Get(oidcURL)
	require.NoError(t, err)
	defer httpResp.Body.Close()

	require.Equal(t, http.StatusOK, httpResp.StatusCode, "Expected 200 OK response from OIDC discovery document but got %d", httpResp.StatusCode)

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
	require.Positive(t, keySet.Len(), "JWKS should contain at least one key")

	// Parse and validate the JWT token using the JWKS
	tkn, err := jwt.Parse([]byte(tokenRaw), jwt.WithKeySet(keySet))
	require.NoError(t, err, "JWT token should be valid and verifiable with JWKS")

	// Validate expected claims
	issuer, ok := tkn.Get("iss")
	require.True(t, ok, "Issuer claim should exist in the token")
	require.Equal(t, discovery.Issuer, issuer, "Expected issuer %s, but got %s", discovery.Issuer, issuer)

	sub, _ := tkn.Get("sub")
	require.Equal(t, myAppID.String(), sub.(string), "Expected subject %s, but got %s", myAppID, sub)

	// Verify token expiration
	expTime, _ := tkn.Get("exp")
	require.NotNil(t, expTime, "Expiration claim should exist")

	// Verify token issuance time
	iatTime, _ := tkn.Get("iat")
	require.NotNil(t, iatTime, "Issued at claim should exist")

	// Verify token audience
	aud, ok := tkn.Get("aud")
	require.True(t, ok, "Audience claim should exist")
	require.NotEmpty(t, aud, "Audience claim should not be empty")
	audList, ok := aud.([]string)
	require.True(t, ok, "Audience claim should be a list of strings")
	require.Contains(t, audList, "public", "Audience claim should contain 'public'")

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
	require.Positive(t, tokenLifetime, "Token lifetime should be positive")
}
