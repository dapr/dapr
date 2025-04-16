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

package ca

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGenerateBundle(t *testing.T) {
	// Create a root key for testing
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	trustDomain := "test.example.com"
	allowedClockSkew := 5 * time.Minute
	overrideTTL := 24 * time.Hour

	t.Run("with x509 only", func(t *testing.T) {
		gen := generate{
			x509: true,
			jwt:  false,
		}

		bundle, err := GenerateBundle(rootKey, trustDomain, allowedClockSkew, &overrideTTL, gen)
		require.NoError(t, err)

		// Verify X.509 components are present
		require.NotEmpty(t, bundle.TrustAnchors)
		require.NotEmpty(t, bundle.IssChainPEM)
		require.NotEmpty(t, bundle.IssKeyPEM)
		require.NotNil(t, bundle.IssChain)
		require.NotNil(t, bundle.IssKey)

		// Verify JWT components are not present
		require.Nil(t, bundle.JWTSigningKey)
		require.Empty(t, bundle.JWTSigningKeyPEM)
		require.Nil(t, bundle.JWKS)
		require.Empty(t, bundle.JWKSJson)

		// Parse the generated certificates to verify them
		rootCert, _ := decodePEM(t, bundle.TrustAnchors)
		require.NotNil(t, rootCert)

		// Verify TTL override was applied
		require.WithinDuration(t, time.Now().Add(overrideTTL), rootCert.NotAfter, time.Minute)
	})

	t.Run("with jwt only", func(t *testing.T) {
		gen := generate{
			x509: false,
			jwt:  true,
		}

		bundle, err := GenerateBundle(rootKey, trustDomain, allowedClockSkew, &overrideTTL, gen)
		require.NoError(t, err)

		// Verify X.509 components are not present
		require.Empty(t, bundle.TrustAnchors)
		require.Empty(t, bundle.IssChainPEM)
		require.Empty(t, bundle.IssKeyPEM)
		require.Nil(t, bundle.IssChain)
		require.Nil(t, bundle.IssKey)

		// Verify JWT components are present
		require.NotNil(t, bundle.JWTSigningKey)
		require.NotEmpty(t, bundle.JWTSigningKeyPEM)
		require.NotNil(t, bundle.JWKS)
		require.NotEmpty(t, bundle.JWKSJson)

		// Parse JWKS to verify it
		var jwksSet map[string]interface{}
		err = json.Unmarshal(bundle.JWKSJson, &jwksSet)
		require.NoError(t, err)

		// Verify key has expected attributes
		keys, ok := jwksSet["keys"].([]interface{})
		require.True(t, ok)
		require.Len(t, keys, 1)

		key := keys[0].(map[string]interface{})
		require.Equal(t, DefaultJWTKeyID, key["kid"])
		require.Equal(t, string(DefaultJWTSignatureAlgorithm), key["alg"])
	})

	t.Run("with both x509 and jwt", func(t *testing.T) {
		gen := generate{
			x509: true,
			jwt:  true,
		}

		bundle, err := GenerateBundle(rootKey, trustDomain, allowedClockSkew, &overrideTTL, gen)
		require.NoError(t, err)

		// Verify X.509 components are present
		require.NotEmpty(t, bundle.TrustAnchors)
		require.NotEmpty(t, bundle.IssChainPEM)
		require.NotEmpty(t, bundle.IssKeyPEM)
		require.NotNil(t, bundle.IssChain)
		require.NotNil(t, bundle.IssKey)

		// Verify JWT components are present
		require.NotNil(t, bundle.JWTSigningKey)
		require.NotEmpty(t, bundle.JWTSigningKeyPEM)
		require.NotNil(t, bundle.JWKS)
		require.NotEmpty(t, bundle.JWKSJson)

		// Parse the JWT key and compare with root key
		require.Equal(t, rootKey, bundle.JWTSigningKey)
	})

	t.Run("with neither x509 nor jwt", func(t *testing.T) {
		gen := generate{
			x509: false,
			jwt:  false,
		}

		bundle, err := GenerateBundle(rootKey, trustDomain, allowedClockSkew, &overrideTTL, gen)
		require.NoError(t, err)

		// Verify bundle is empty
		require.Empty(t, bundle.TrustAnchors)
		require.Empty(t, bundle.IssChainPEM)
		require.Empty(t, bundle.IssKeyPEM)
		require.Nil(t, bundle.IssChain)
		require.Nil(t, bundle.IssKey)
		require.Nil(t, bundle.JWTSigningKey)
		require.Empty(t, bundle.JWTSigningKeyPEM)
		require.Nil(t, bundle.JWKS)
		require.Empty(t, bundle.JWKSJson)
	})

	t.Run("with non-ECDSA key", func(t *testing.T) {
		// Create a mock non-ECDSA key for testing
		mockKey := &mockSigner{}
		gen := generate{
			x509: true,
			jwt:  true,
		}

		_, err := GenerateBundle(mockKey, trustDomain, allowedClockSkew, &overrideTTL, gen)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to sign root certificate")
	})

	t.Run("with nil TTL override", func(t *testing.T) {
		gen := generate{
			x509: true,
			jwt:  false,
		}

		bundle, err := GenerateBundle(rootKey, trustDomain, allowedClockSkew, nil, gen)
		require.NoError(t, err)

		// Verify X.509 components are present with default TTL
		block, _ := decodePEM(t, bundle.TrustAnchors)
		rootCert, err := x509.ParseCertificate(block.Raw)
		require.NoError(t, err)

		// Default TTL is typically 1 year
		expectedDefaultTTL := time.Hour * 24 * 365
		require.WithinDuration(t, time.Now().Add(expectedDefaultTTL), rootCert.NotAfter, time.Hour*24)
	})

	t.Run("bundle equality", func(t *testing.T) {
		gen := generate{
			x509: true,
			jwt:  true,
		}

		bundle1, err := GenerateBundle(rootKey, trustDomain, allowedClockSkew, &overrideTTL, gen)
		require.NoError(t, err)

		// Same bundle should equal itself
		require.True(t, bundle1.Equals(bundle1))

		// Different bundle should not equal
		bundle2, err := GenerateBundle(rootKey, trustDomain, allowedClockSkew, &overrideTTL, gen)
		require.NoError(t, err)
		require.False(t, bundle1.Equals(bundle2))
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

// Mock crypto.Signer for testing non-ECDSA key case
type mockSigner struct{}

func (m *mockSigner) Public() crypto.PublicKey {
	return nil
}

func (m *mockSigner) Sign(rand io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	return nil, nil
}
