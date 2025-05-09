/*
Copyright 2023 The Dapr Authors
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

package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/dapr/kit/crypto/pem"
	"github.com/lestrrat-go/jwx/jwk"
	"github.com/lestrrat-go/jwx/jwt"
)

func TestNew(t *testing.T) {
	t.Run("if no existing bundle exist, new should generate a new bundle", func(t *testing.T) {
		t.Setenv("NAMESPACE", "dapr-test")
		t.Cleanup(func() {
			os.Unsetenv("NAMESPACE")
		})

		dir := t.TempDir()
		rootCertPath := filepath.Join(dir, "root.cert")
		issuerCertPath := filepath.Join(dir, "issuer.cert")
		issuerKeyPath := filepath.Join(dir, "issuer.key")
		config := config.Config{
			RootCertPath:   rootCertPath,
			IssuerCertPath: issuerCertPath,
			IssuerKeyPath:  issuerKeyPath,
			JWTEnabled:     true,
			TrustDomain:    "test.example.com",
			Mode:           modes.StandaloneMode,
		}

		_, err := New(t.Context(), config)
		require.NoError(t, err)

		// Check that all certificate and key files were generated
		require.FileExists(t, rootCertPath)
		require.FileExists(t, issuerCertPath)
		require.FileExists(t, issuerKeyPath)

		rootCert, err := os.ReadFile(rootCertPath)
		require.NoError(t, err)
		issuerCert, err := os.ReadFile(issuerCertPath)
		require.NoError(t, err)
		issuerKey, err := os.ReadFile(issuerKeyPath)
		require.NoError(t, err)

		// Verify certificate content
		rootCertX509, err := pem.DecodePEMCertificates(rootCert)
		require.NoError(t, err)
		require.Len(t, rootCertX509, 1)
		assert.Equal(t, []string{"test.example.com"}, rootCertX509[0].Subject.Organization)

		issuerCertX509, err := pem.DecodePEMCertificates(issuerCert)
		require.NoError(t, err)
		require.Len(t, issuerCertX509, 1)
		assert.Equal(t, []string{"spiffe://test.example.com/ns/dapr-test/dapr-sentry"}, issuerCertX509[0].Subject.Organization)

		issuerKeyPK, err := pem.DecodePEMPrivateKey(issuerKey)
		require.NoError(t, err)

		require.NoError(t, issuerCertX509[0].CheckSignatureFrom(rootCertX509[0]))
		ok, err := pem.PublicKeysEqual(issuerCertX509[0].PublicKey, issuerKeyPK.Public())
		require.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("if no existing bundle exist, new should generate a new bundle with jwt", func(t *testing.T) {
		t.Setenv("NAMESPACE", "dapr-test")
		t.Cleanup(func() {
			os.Unsetenv("NAMESPACE")
		})

		dir := t.TempDir()
		rootCertPath := filepath.Join(dir, "root.cert")
		issuerCertPath := filepath.Join(dir, "issuer.cert")
		issuerKeyPath := filepath.Join(dir, "issuer.key")
		jwksPath := filepath.Join(dir, "jwks.json")
		jwtKeyPath := filepath.Join(dir, "jwt.key")
		config := config.Config{
			RootCertPath:        rootCertPath,
			IssuerCertPath:      issuerCertPath,
			IssuerKeyPath:       issuerKeyPath,
			JWTEnabled:          true,
			JWKSPath:            jwksPath,
			JWTSigningKeyPath:   jwtKeyPath,
			JWTSigningAlgorithm: DefaultJWTSignatureAlgorithm.String(),
			JWTKeyID:            DefaultJWTKeyID,
			TrustDomain:         "test.example.com",
			Mode:                modes.StandaloneMode,
		}

		caObj, err := New(t.Context(), config)
		require.NoError(t, err)

		// Check that all certificate and key files were generated
		require.FileExists(t, rootCertPath)
		require.FileExists(t, issuerCertPath)
		require.FileExists(t, issuerKeyPath)
		require.FileExists(t, jwksPath)
		require.FileExists(t, jwtKeyPath)

		rootCert, err := os.ReadFile(rootCertPath)
		require.NoError(t, err)
		issuerCert, err := os.ReadFile(issuerCertPath)
		require.NoError(t, err)
		issuerKey, err := os.ReadFile(issuerKeyPath)
		require.NoError(t, err)
		jwksData, err := os.ReadFile(jwksPath)
		require.NoError(t, err)
		jwtKey, err := os.ReadFile(jwtKeyPath)
		require.NoError(t, err)

		// Verify certificate content
		rootCertX509, err := pem.DecodePEMCertificates(rootCert)
		require.NoError(t, err)
		require.Len(t, rootCertX509, 1)
		assert.Equal(t, []string{"test.example.com"}, rootCertX509[0].Subject.Organization)

		issuerCertX509, err := pem.DecodePEMCertificates(issuerCert)
		require.NoError(t, err)
		require.Len(t, issuerCertX509, 1)
		assert.Equal(t, []string{"spiffe://test.example.com/ns/dapr-test/dapr-sentry"}, issuerCertX509[0].Subject.Organization)

		issuerKeyPK, err := pem.DecodePEMPrivateKey(issuerKey)
		require.NoError(t, err)

		require.NoError(t, issuerCertX509[0].CheckSignatureFrom(rootCertX509[0]))
		ok, err := pem.PublicKeysEqual(issuerCertX509[0].PublicKey, issuerKeyPK.Public())
		require.NoError(t, err)
		assert.True(t, ok)

		// Verify JWT content
		require.NotEmpty(t, jwksData)
		jwks, err := jwk.Parse(jwksData)
		require.NoError(t, err)
		require.NotNil(t, jwks)

		// JWKS should have exactly one key
		require.Equal(t, 1, jwks.Len(), "JWKS should contain exactly one key")

		// Key should have the expected key ID and algorithm
		key, ok := jwks.Get(0)
		require.True(t, ok, "JWKS should contain a key")
		keyID := key.KeyID()
		require.Equal(t, DefaultJWTKeyID, keyID)

		alg := key.Algorithm()
		require.Equal(t, DefaultJWTSignatureAlgorithm.String(), alg)

		// JWT key should be valid
		jwtKeyPK, err := pem.DecodePEMPrivateKey(jwtKey)
		require.NoError(t, err)
		require.NotNil(t, jwtKeyPK)

		// Verify JWT functionality by generating a token
		caInstance := caObj.(*ca)
		require.NotNil(t, caInstance.jwtIssuer)

		// Generate a JWT
		jwtToken, err := caInstance.GenerateJWT(t.Context(), &JWTRequest{
			Audience:  "test.example.com",
			Namespace: "test-namespace",
			AppID:     "test-app",
			TTL:       time.Hour,
		})
		require.NoError(t, err)
		require.NotEmpty(t, jwtToken)

		// Parse and verify the token
		parsedToken, err := jwt.Parse([]byte(jwtToken),
			jwt.WithKeySet(jwks),
			jwt.WithValidate(true))
		require.NoError(t, err)

		// Verify token claims
		sub, ok := parsedToken.Get("sub")
		require.True(t, ok, "subject claim should exist")
		require.Equal(t, "spiffe://test.example.com/ns/test-namespace/test-app", sub)
	})

	t.Run("if existing pool exists, new should load the existing pool", func(t *testing.T) {
		dir := t.TempDir()
		rootCertPath := filepath.Join(dir, "root.cert")
		issuerCertPath := filepath.Join(dir, "issuer.cert")
		issuerKeyPath := filepath.Join(dir, "issuer.key")
		config := config.Config{
			RootCertPath:   rootCertPath,
			IssuerCertPath: issuerCertPath,
			IssuerKeyPath:  issuerKeyPath,
			Mode:           modes.StandaloneMode,
		}

		rootPEM, rootCrt, _, rootPK := genCrt(t, "root", nil, nil)
		rootPEM2, _, _, _ := genCrt(t, "root2", nil, nil)
		int1PEM, int1Crt, _, int1PK := genCrt(t, "int1", rootCrt, rootPK)
		int2PEM, int2Crt, int2PKPEM, int2PK := genCrt(t, "int2", int1Crt, int1PK)

		//nolint:gocritic
		rootFileContents := append(rootPEM, rootPEM2...)
		//nolint:gocritic
		issuerFileContents := append(int2PEM, int1PEM...)
		issuerKeyFileContents := int2PKPEM

		require.NoError(t, os.WriteFile(rootCertPath, rootFileContents, 0o600))
		require.NoError(t, os.WriteFile(issuerCertPath, issuerFileContents, 0o600))
		require.NoError(t, os.WriteFile(issuerKeyPath, issuerKeyFileContents, 0o600))

		caImp, err := New(t.Context(), config)
		require.NoError(t, err)

		rootCert, err := os.ReadFile(rootCertPath)
		require.NoError(t, err)
		issuerCert, err := os.ReadFile(issuerCertPath)
		require.NoError(t, err)
		issuerKey, err := os.ReadFile(issuerKeyPath)
		require.NoError(t, err)

		assert.Equal(t, rootFileContents, rootCert)
		assert.Equal(t, issuerFileContents, issuerCert)
		assert.Equal(t, issuerKeyFileContents, issuerKey)

		assert.Equal(t, Bundle{
			TrustAnchors: rootFileContents,
			IssChainPEM:  issuerFileContents,
			IssKeyPEM:    issuerKeyFileContents,
			IssChain:     []*x509.Certificate{int2Crt, int1Crt},
			IssKey:       int2PK,
		}, caImp.(*ca).bundle)
	})
}

func TestSignIdentity(t *testing.T) {
	t.Run("singing identity should return a signed certificate with chain", func(t *testing.T) {
		dir := t.TempDir()
		rootCertPath := filepath.Join(dir, "root.cert")
		issuerCertPath := filepath.Join(dir, "issuer.cert")
		issuerKeyPath := filepath.Join(dir, "issuer.key")
		config := config.Config{
			RootCertPath:   rootCertPath,
			IssuerCertPath: issuerCertPath,
			IssuerKeyPath:  issuerKeyPath,
		}

		rootPEM, rootCrt, _, rootPK := genCrt(t, "root", nil, nil)
		rootPEM2, _, _, _ := genCrt(t, "root2", nil, nil)
		int1PEM, int1Crt, _, int1PK := genCrt(t, "int1", rootCrt, rootPK)
		int2PEM, int2Crt, int2PKPEM, _ := genCrt(t, "int2", int1Crt, int1PK)

		//nolint:gocritic
		rootFileContents := append(rootPEM, rootPEM2...)
		//nolint:gocritic
		issuerFileContents := append(int2PEM, int1PEM...)
		issuerKeyFileContents := int2PKPEM

		require.NoError(t, os.WriteFile(rootCertPath, rootFileContents, 0o600))
		require.NoError(t, os.WriteFile(issuerCertPath, issuerFileContents, 0o600))
		require.NoError(t, os.WriteFile(issuerKeyPath, issuerKeyFileContents, 0o600))

		ca, err := New(t.Context(), config)
		require.NoError(t, err)

		clientPK, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)

		clientCert, err := ca.SignIdentity(t.Context(), &SignRequest{
			PublicKey:          clientPK.Public(),
			SignatureAlgorithm: x509.ECDSAWithSHA256,
			TrustDomain:        "example.test.dapr.io",
			Namespace:          "my-test-namespace",
			AppID:              "my-app-id",
			DNS:                []string{"my-app-id.my-test-namespace.svc.cluster.local", "example.com"},
		})
		require.NoError(t, err)

		require.Len(t, clientCert, 3)
		assert.Equal(t, clientCert[1], int2Crt)
		assert.Equal(t, clientCert[2], int1Crt)

		assert.Len(t, clientCert[0].DNSNames, 2)
		assert.ElementsMatch(t, clientCert[0].DNSNames, []string{"my-app-id.my-test-namespace.svc.cluster.local", "example.com"})

		require.Len(t, clientCert[0].URIs, 1)
		assert.Equal(t, "spiffe://example.test.dapr.io/ns/my-test-namespace/my-app-id", clientCert[0].URIs[0].String())

		require.NoError(t, clientCert[0].CheckSignatureFrom(int2Crt))
	})
}
