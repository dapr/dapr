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

package bundle

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateX509Bundle(t *testing.T) {
	// Create a root key for testing
	_, x509RootKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	trustDomain := "test.example.com"
	allowedClockSkew := 5 * time.Minute
	overrideTTL := 24 * time.Hour

	t.Run("with x509", func(t *testing.T) {
		bundle, err := GenerateX509(OptionsX509{
			X509RootKey:      x509RootKey,
			TrustDomain:      trustDomain,
			AllowedClockSkew: allowedClockSkew,
			OverrideCATTL:    &overrideTTL,
		})
		require.NoError(t, err)

		// Verify X.509 components are present
		require.NotEmpty(t, bundle.TrustAnchors)
		require.NotEmpty(t, bundle.IssChainPEM)
		require.NotEmpty(t, bundle.IssKeyPEM)
		require.NotNil(t, bundle.IssChain)
		require.NotNil(t, bundle.IssKey)

		// Parse the generated certificates to verify them
		rootCert, _ := decodePEM(t, bundle.TrustAnchors)
		require.NotNil(t, rootCert)

		// Verify TTL override was applied
		require.WithinDuration(t, time.Now().Add(overrideTTL), rootCert.NotAfter, time.Minute)
	})

	t.Run("with nil TTL override", func(t *testing.T) {
		bundle, err := GenerateX509(OptionsX509{
			X509RootKey:      x509RootKey,
			TrustDomain:      trustDomain,
			AllowedClockSkew: allowedClockSkew,
			OverrideCATTL:    nil,
		})
		require.NoError(t, err)

		// Verify X.509 components are present with default TTL
		block, _ := decodePEM(t, bundle.TrustAnchors)
		rootCert, err := x509.ParseCertificate(block.Raw)
		require.NoError(t, err)

		// Default TTL is typically 1 year
		expectedDefaultTTL := time.Hour * 24 * 365
		require.WithinDuration(t, time.Now().Add(expectedDefaultTTL), rootCert.NotAfter, time.Hour*24)
	})
}

func TestGenerateX509Bundle_ECFields(t *testing.T) {
	_, x509RootKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	bundle, err := GenerateX509(OptionsX509{
		X509RootKey:      x509RootKey,
		TrustDomain:      "test.example.com",
		AllowedClockSkew: 5 * time.Minute,
	})
	require.NoError(t, err)

	// EC fields should be populated
	require.NotEmpty(t, bundle.ECTrustAnchors, "ECTrustAnchors should be populated")
	require.NotEmpty(t, bundle.ECIssChainPEM, "ECIssChainPEM should be populated")
	require.NotEmpty(t, bundle.ECIssKeyPEM, "ECIssKeyPEM should be populated")
	require.NotNil(t, bundle.ECIssChain, "ECIssChain should be populated")
	require.Len(t, bundle.ECIssChain, 1)
	require.NotNil(t, bundle.ECIssKey, "ECIssKey should be populated")

	// EC trust anchors should be different from main trust anchors
	assert.NotEqual(t, bundle.TrustAnchors, bundle.ECTrustAnchors,
		"ECDSA trust anchors should be independent from Ed25519 trust anchors")

	// EC root should be self-signed ECDSA
	ecRoot, _ := decodePEM(t, bundle.ECTrustAnchors)
	require.NotNil(t, ecRoot)
	assert.IsType(t, &ecdsa.PublicKey{}, ecRoot.PublicKey, "EC root should use ECDSA key")
	require.NoError(t, ecRoot.CheckSignatureFrom(ecRoot), "EC root should be self-signed")

	// EC issuer should be signed by EC root (not Ed25519 root)
	require.NoError(t, bundle.ECIssChain[0].CheckSignatureFrom(ecRoot),
		"EC issuer should be signed by EC root")
	assert.IsType(t, &ecdsa.PublicKey{}, bundle.ECIssChain[0].PublicKey,
		"EC issuer should use ECDSA key")

	// Main issuer should still be Ed25519
	mainRoot, _ := decodePEM(t, bundle.TrustAnchors)
	assert.IsType(t, ed25519.PublicKey{}, mainRoot.PublicKey,
		"Main root should remain Ed25519")
}

func TestGenerateECX509(t *testing.T) {
	bundle, err := GenerateECX509(OptionsX509{
		TrustDomain:      "test.example.com",
		AllowedClockSkew: 5 * time.Minute,
	})
	require.NoError(t, err)

	require.NotEmpty(t, bundle.TrustAnchors)
	require.NotEmpty(t, bundle.IssChainPEM)
	require.NotEmpty(t, bundle.IssKeyPEM)
	require.NotNil(t, bundle.IssChain)
	require.Len(t, bundle.IssChain, 1)
	require.NotNil(t, bundle.IssKey)

	// Root should be self-signed ECDSA
	root, _ := decodePEM(t, bundle.TrustAnchors)
	require.NotNil(t, root)
	assert.IsType(t, &ecdsa.PublicKey{}, root.PublicKey)
	assert.True(t, root.IsCA)
	require.NoError(t, root.CheckSignatureFrom(root))

	// Issuer should be signed by ECDSA root
	require.NoError(t, bundle.IssChain[0].CheckSignatureFrom(root))
	assert.IsType(t, &ecdsa.PublicKey{}, bundle.IssChain[0].PublicKey)
	assert.True(t, bundle.IssChain[0].IsCA)

	// Full chain verification: sign a leaf cert and verify chain
	leafKey, err := ecdsa.GenerateKey(bundle.IssChain[0].PublicKey.(*ecdsa.PublicKey).Curve, rand.Reader)
	require.NoError(t, err)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(99),
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, bundle.IssChain[0], &leafKey.PublicKey, bundle.IssKey)
	require.NoError(t, err)
	leaf, err := x509.ParseCertificate(leafDER)
	require.NoError(t, err)

	// Verify leaf → issuer → root chain
	rootPool := x509.NewCertPool()
	rootPool.AddCert(root)
	intPool := x509.NewCertPool()
	intPool.AddCert(bundle.IssChain[0])
	_, err = leaf.Verify(x509.VerifyOptions{
		Roots:         rootPool,
		Intermediates: intPool,
	})
	require.NoError(t, err, "Full ECDSA chain should verify without Ed25519")
}

func TestGenerateJWTBundle(t *testing.T) {
	// Create a separate JWT key for testing
	jwtRootKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	trustDomain := "test.example.com"

	t.Run("with jwt", func(t *testing.T) {
		bundle, err := GenerateJWT(OptionsJWT{
			JWTRootKey:  jwtRootKey,
			TrustDomain: trustDomain,
		})
		require.NoError(t, err)

		// Verify JWT components are present
		require.NotNil(t, bundle.SigningKey)
		require.NotEmpty(t, bundle.SigningKeyPEM)
		require.NotNil(t, bundle.JWKS)
		require.NotEmpty(t, bundle.JWKSJson)

		// Parse JWKS to verify it
		var jwksSet map[string]any
		err = json.Unmarshal(bundle.JWKSJson, &jwksSet)
		require.NoError(t, err)

		// Verify key has expected attributes
		keys, ok := jwksSet["keys"].([]any)
		require.True(t, ok)
		require.Len(t, keys, 1)

		k, err := jwk.FromRaw(bundle.SigningKey)
		require.NoError(t, err)
		tp, err := k.Thumbprint(DefaultKeyThumbprintAlgorithm)
		require.NoError(t, err)
		key := keys[0].(map[string]any)
		require.Equal(t, base64.StdEncoding.EncodeToString(tp), key["kid"])
		require.Equal(t, string(DefaultJWTSignatureAlgorithm), key["alg"])
	})
}

// Helper to decode PEM
func decodePEM(t *testing.T, pemData []byte) (*x509.Certificate, *x509.Certificate) {
	block, rest := pem.Decode(pemData)
	require.NotNil(t, block)

	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	var issuer *x509.Certificate
	if len(rest) > 0 {
		block, _ = pem.Decode(rest)
		if block != nil {
			issuer, err = x509.ParseCertificate(block.Bytes)
			require.NoError(t, err)
		}
	}

	return cert, issuer
}
