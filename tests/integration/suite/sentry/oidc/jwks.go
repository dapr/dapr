package oidc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"
	jwxjwt "github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() { suite.Register(new(customJWKSTest)) }

// customJWKSTest validates providing a custom JWT signing key + JWKS.
type customJWKSTest struct {
	sentry     *sentry.Sentry
	jwksBytes  []byte
	jwksKeyID  string
	privKeyPEM []byte
	baseURL    string
	certFile   string
	keyFile    string
	httpClient *http.Client
}

func (c *customJWKSTest) Setup(t *testing.T) []framework.Option {
	// Generate X509+custom JWT materials
	x509Bundle := c.generateX509Bundle(t)
	privKey, pubJWK, jwksJSON, kid := c.generateJWTKeyAndJWKS(t)
	c.jwksBytes = jwksJSON
	c.jwksKeyID = kid

	// Marshal private key to PKCS8 (PEM)
	der, err := x509.MarshalPKCS8PrivateKey(privKey)
	require.NoError(t, err)
	c.privKeyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	// Set JWT data on bundle
	set := jwk.NewSet()
	set.AddKey(pubJWK)
	x509Bundle.JWT = &bundle.JWT{
		SigningKey:    privKey,
		SigningKeyPEM: c.privKeyPEM,
		JWKS:          set,
		JWKSJson:      jwksJSON,
	}

	// TLS cert for OIDC
	cert, key := c.generateTLSServerCert(t)
	tmpDir := t.TempDir()
	c.certFile = filepath.Join(tmpDir, "oidc.crt")
	c.keyFile = filepath.Join(tmpDir, "oidc.key")
	require.NoError(t, os.WriteFile(c.certFile, cert, 0o600))
	require.NoError(t, os.WriteFile(c.keyFile, key, 0o600))

	// Start Sentry with custom bundle
	c.sentry = sentry.New(t,
		sentry.WithMode("standalone"),
		sentry.WithCABundle(x509Bundle), // use our custom bundle
		sentry.WithEnableJWT(true),
		sentry.WithJWTIssuerFromOIDC(),
		sentry.WithJWTTTL(1*time.Hour),
		sentry.WithOIDCEnabled(true),
		sentry.WithOIDCTLSCertFile(c.certFile),
		sentry.WithOIDCTLSKeyFile(c.keyFile),
	)

	return []framework.Option{framework.WithProcesses(c.sentry)}
}

func (c *customJWKSTest) Run(t *testing.T, ctx context.Context) {
	c.sentry.WaitUntilRunning(t, ctx)

	// Build base URL (TLS enabled)
	port := c.sentry.OIDCPort(t)
	c.baseURL = fmt.Sprintf("https://localhost:%d", port)
	c.httpClient = c.createHTTPClientWithCustomCA(t)

	c.testServedJWKSMatchesProvided(t)
	c.testIssuedTokenMatchesJWKS(t, ctx)
	c.testInvalidSentryJWKSSetup(t, ctx)
	c.testExplicitUserProvidedKID(t, ctx)
}

// testServedJWKSMatchesProvided ensures the OIDC JWKS endpoint serves the provided JWKS (kid + key material)
func (c *customJWKSTest) testServedJWKSMatchesProvided(t *testing.T) {
	resp, err := c.httpClient.Get(c.baseURL + "/jwks.json")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var served struct {
		Keys []map[string]any `json:"keys"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&served))
	require.Len(t, served.Keys, 1)
	key := served.Keys[0]
	assert.Equal(t, c.jwksKeyID, key["kid"])
	assert.Equal(t, "sig", key["use"])   // usage
	assert.Equal(t, "RSA", key["kty"])   // type
	assert.Equal(t, "RS256", key["alg"]) // algorithm
	// Basic sanity that modulus/exponent exist
	assert.NotEmpty(t, key["n"])
	assert.NotEmpty(t, key["e"])

	// Decode original provided JWKS and compare modulus and exponent exactly
	var original struct {
		Keys []map[string]any `json:"keys"`
	}
	require.NoError(t, json.Unmarshal(c.jwksBytes, &original))
	require.Len(t, original.Keys, 1)
	origKey := original.Keys[0]
	origN, okN := origKey["n"].(string)
	origE, okE := origKey["e"].(string)
	require.True(t, okN && okE, "original JWKS n/e should be strings")
	servedN, okSN := key["n"].(string)
	servedE, okSE := key["e"].(string)
	require.True(t, okSN && okSE, "served JWKS n/e should be strings")
	assert.Equal(t, origN, servedN, "RSA modulus (n) must match original provided JWKS")
	assert.Equal(t, origE, servedE, "RSA exponent (e) must match original provided JWKS")
}

// testIssuedTokenMatchesJWKS requests a certificate with JWT and validates signature against served JWKS
func (c *customJWKSTest) testIssuedTokenMatchesJWKS(t *testing.T, ctx context.Context) {
	conn := c.sentry.DialGRPC(t, ctx, "spiffe://localhost/ns/default/dapr-sentry")
	defer conn.Close()
	client := sentryv1pb.NewCAClient(conn)

	// Create CSR
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "custom-app"}}, priv)
	require.NoError(t, err)
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})

	res, err := client.SignCertificate(ctx, &sentryv1pb.SignCertificateRequest{
		Id:                        "custom-app",
		Namespace:                 "default",
		TrustDomain:               "localhost",
		CertificateSigningRequest: csrPEM,
		TokenValidator:            sentryv1pb.SignCertificateRequest_INSECURE,
		JwtAudiences:              []string{"custom-aud"},
	})
	require.NoError(t, err)
	jwtTok := res.GetJwt()
	require.NotNil(t, jwtTok)
	require.NotEmpty(t, jwtTok.GetValue())

	// Parse served JWKS
	set, err := jwk.Parse(c.jwksBytes)
	require.NoError(t, err)

	// Validate JWT signature & claims using JWX library
	tok, err := jwxjwt.Parse([]byte(jwtTok.GetValue()), jwxjwt.WithKeySet(set), jwxjwt.WithValidate(true))
	require.NoError(t, err, "token must validate against provided JWKS")
	aud := tok.Audience()
	assert.Contains(t, aud, "custom-aud")
	assert.Contains(t, aud, "localhost") // trust domain automatically added
	expectedSub := "spiffe://localhost/ns/default/" + "custom-app"
	assert.Equal(t, expectedSub, tok.Subject())

	// Decode header to assert kid
	parts := strings.Split(jwtTok.GetValue(), ".")
	require.Len(t, parts, 3)
	headBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	require.NoError(t, err)
	var header map[string]any
	require.NoError(t, json.Unmarshal(headBytes, &header))
	assert.Equal(t, c.jwksKeyID, header["kid"])
	assert.Equal(t, "RS256", header["alg"])
}

// testInvalidSentryJWKSSetup spins up ephemeral Sentry instances that should fail initialization
func (c *customJWKSTest) testInvalidSentryJWKSSetup(t *testing.T, parentCtx context.Context) {
	// Helper to build a base X509 bundle
	buildBase := func() bundle.Bundle { return c.generateX509Bundle(t) }

	// Valid signing key for baseline
	validPriv, pubJWK, validJWKS, _ := c.generateJWTKeyAndJWKS(t)

	tests := []struct {
		name string
		mut  func(b *bundle.Bundle)
		msg  string
	}{
		{
			name: "missing jwks json",
			mut: func(b *bundle.Bundle) {
				b.JWT.SigningKey = validPriv
				der, _ := x509.MarshalPKCS8PrivateKey(validPriv)
				b.JWT.SigningKeyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
				// Leave JWKS/JWKSJson nil -> empty file written -> parse failure
			},
			msg: "should fail without JWKS",
		},
		{
			name: "empty jwks set",
			mut: func(b *bundle.Bundle) {
				b.JWT.SigningKey = validPriv
				der, _ := x509.MarshalPKCS8PrivateKey(validPriv)
				b.JWT.SigningKeyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
				emptySet := jwk.NewSet()
				js, _ := json.Marshal(emptySet)
				b.JWT.JWKS = emptySet
				b.JWT.JWKSJson = js
			},
			msg: "should fail with empty JWKS",
		},
		{
			name: "mismatched jwks key",
			mut: func(b *bundle.Bundle) {
				// valid signing key
				b.JWT.SigningKey = validPriv
				der, _ := x509.MarshalPKCS8PrivateKey(validPriv)
				b.JWT.SigningKeyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
				// Different key for JWKS
				otherPriv, _ := rsa.GenerateKey(rand.Reader, 2048)
				otherJWK, _ := jwk.FromRaw(&otherPriv.PublicKey)
				otherJWK.Set(jwk.KeyUsageKey, "sig")
				otherJWK.Set(jwk.AlgorithmKey, "RS256")
				otherJWK.Set(jwk.KeyIDKey, "other-key")
				set := jwk.NewSet()
				set.AddKey(otherJWK)
				js, _ := json.Marshal(set)
				b.JWT.JWKS = set
				b.JWT.JWKSJson = js
			},
			msg: "should fail when JWKS public key does not match signing key",
		},
	}

	for _, tc := range tests {
		b := buildBase()
		priv, pub, jwksJSON, kid := validPriv, pubJWK, validJWKS, ""
		_ = priv
		_ = pub
		_ = jwksJSON
		_ = kid
		b.JWT = new(bundle.JWT)
		if tc.mut != nil {
			tc.mut(&b)
		}

		t.Run(tc.name, func(t *testing.T) {
			cert, key := c.generateTLSServerCert(t)
			certPath := filepath.Join(t.TempDir(), "oidc.crt")
			keyPath := filepath.Join(t.TempDir(), "oidc.key")
			require.NoError(t, os.WriteFile(certPath, cert, 0o600))
			require.NoError(t, os.WriteFile(keyPath, key, 0o600))

			s := sentry.New(t,
				sentry.WithMode("standalone"),
				sentry.WithCABundle(b),
				sentry.WithEnableJWT(true),
				sentry.WithJWTIssuer("https://localhost:9443"),
				sentry.WithOIDCEnabled(true),
				sentry.WithOIDCTLSCertFile(certPath),
				sentry.WithOIDCTLSKeyFile(keyPath),
				sentry.WithExecOptions(
					exec.WithExitCode(1),
					exec.WithRunError(func(t *testing.T, err error) { require.ErrorContains(t, err, "exit status 1") }),
				),
			)

			cctx, cancel := context.WithTimeout(parentCtx, 5*time.Second)
			defer cancel()
			s.Run(t, cctx)

			// Give the process a moment to validate config and exit.
			time.Sleep(1 * time.Second)
			s.Cleanup(t)
		})
	}
}

// testExplicitUserProvidedKID tests that a user-provided key ID is used as-is
func (c *customJWKSTest) testExplicitUserProvidedKID(t *testing.T, ctx context.Context) {
	t.Run("explicit_user_kid", func(t *testing.T) {
		// Build bundle with custom kid and pass kid explicitly to Sentry
		x509Bundle := c.generateX509Bundle(t)
		priv, pubJWK, _, _ := c.generateJWTKeyAndJWKS(t)

		// Override kid to a deterministic custom value (not matching thumbprint)
		customKID := fmt.Sprintf("user-kid-%d", time.Now().UnixNano())
		require.NoError(t, pubJWK.Set(jwk.KeyIDKey, customKID))
		set := jwk.NewSet()
		set.AddKey(pubJWK)
		jwksJSON, err := json.Marshal(set)
		require.NoError(t, err)
		der, err := x509.MarshalPKCS8PrivateKey(priv)
		require.NoError(t, err)
		x509Bundle.JWT = new(bundle.JWT)
		x509Bundle.JWT.SigningKey = priv
		x509Bundle.JWT.SigningKeyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
		x509Bundle.JWT.JWKS = set
		x509Bundle.JWT.JWKSJson = jwksJSON

		cert, key := c.generateTLSServerCert(t)
		td := t.TempDir()
		certPath := filepath.Join(td, "oidc.crt")
		keyPath := filepath.Join(td, "oidc.key")
		require.NoError(t, os.WriteFile(certPath, cert, 0o600))
		require.NoError(t, os.WriteFile(keyPath, key, 0o600))

		s := sentry.New(t,
			sentry.WithMode("standalone"),
			sentry.WithCABundle(x509Bundle),
			sentry.WithEnableJWT(true),
			sentry.WithJWTIssuerFromOIDC(),
			sentry.WithOIDCEnabled(true),
			sentry.WithOIDCTLSCertFile(certPath),
			sentry.WithOIDCTLSKeyFile(keyPath),
			sentry.WithJWTKeyID(customKID),
		)

		ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		s.Run(t, ctx2)
		s.WaitUntilRunning(t, ctx2)

		// Query JWKS endpoint and ensure kid matches customKID
		client := c.createHTTPClientWithCustomCert(t, certPath)
		resp, err := client.Get(fmt.Sprintf("https://localhost:%d/jwks.json", s.OIDCPort(t)))
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		var served struct {
			Keys []map[string]any `json:"keys"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&served))
		require.Len(t, served.Keys, 1)
		assert.Equal(t, customKID, served.Keys[0]["kid"], "served kid should be the user-provided kid, not thumbprint")

		// Request JWT and verify header kid unchanged
		conn := s.DialGRPC(t, ctx2, "spiffe://localhost/ns/default/dapr-sentry")
		defer conn.Close()
		clientGRPC := sentryv1pb.NewCAClient(conn)
		privEC, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)
		csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "explicit-app"}}, privEC)
		require.NoError(t, err)
		csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
		res, err := clientGRPC.SignCertificate(ctx2, &sentryv1pb.SignCertificateRequest{Id: "explicit-app", Namespace: "default", TrustDomain: "localhost", CertificateSigningRequest: csrPEM, TokenValidator: sentryv1pb.SignCertificateRequest_INSECURE, JwtAudiences: []string{"exp-aud"}})
		require.NoError(t, err)
		jwtTok := res.GetJwt()
		require.NotNil(t, jwtTok)
		require.NotEmpty(t, jwtTok.GetValue())
		parts := strings.Split(jwtTok.GetValue(), ".")
		require.Len(t, parts, 3)
		headBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
		require.NoError(t, err)
		var header map[string]any
		require.NoError(t, json.Unmarshal(headBytes, &header))
		assert.Equal(t, customKID, header["kid"], "JWT header kid must remain user-provided kid")
	})
}

// Helper: generate only X509 portion of bundle
func (c *customJWKSTest) generateX509Bundle(t *testing.T) bundle.Bundle {
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	x509bundle, err := bundle.GenerateX509(bundle.OptionsX509{
		X509RootKey:      rootKey,
		TrustDomain:      "integration.test.dapr.io",
		AllowedClockSkew: time.Second * 20,
		OverrideCATTL:    nil,
	})
	require.NoError(t, err)
	return bundle.Bundle{
		X509: x509bundle,
	}
}

// Helper: generate RSA signing key + JWKS JSON
func (c *customJWKSTest) generateJWTKeyAndJWKS(t *testing.T) (*rsa.PrivateKey, jwk.Key, []byte, string) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	pubJWK, err := jwk.FromRaw(&priv.PublicKey)
	require.NoError(t, err)
	require.NoError(t, pubJWK.Set(jwk.KeyUsageKey, "sig"))
	require.NoError(t, pubJWK.Set(jwk.AlgorithmKey, "RS256"))
	thumb, err := pubJWK.Thumbprint(bundle.DefaultKeyThumbprintAlgorithm)
	require.NoError(t, err)
	kid := base64.StdEncoding.EncodeToString(thumb)
	require.NoError(t, pubJWK.Set(jwk.KeyIDKey, kid))
	set := jwk.NewSet()
	set.AddKey(pubJWK)
	jwksJSON, err := json.Marshal(set)
	require.NoError(t, err)
	return priv, pubJWK, jwksJSON, kid
}

// TLS server cert for OIDC
func (c *customJWKSTest) generateTLSServerCert(t *testing.T) ([]byte, []byte) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{Organization: []string{"Dapr Test"}},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

func (c *customJWKSTest) createHTTPClientWithCustomCA(t *testing.T) *http.Client {
	certPEM, err := os.ReadFile(c.certFile)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	ok := pool.AppendCertsFromPEM(certPEM)
	require.True(t, ok)
	return &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}, Timeout: 15 * time.Second}
}

// Helper for custom cert root pool
func (c *customJWKSTest) createHTTPClientWithCustomCert(t *testing.T, certPath string) *http.Client {
	certPEM, err := os.ReadFile(certPath)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	ok := pool.AppendCertsFromPEM(certPEM)
	require.True(t, ok)
	return &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}, Timeout: 15 * time.Second}
}
