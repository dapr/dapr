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
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/stretchr/testify/require"
)

func TestGenerateBundle(t *testing.T) {
	// Create a root key for testing
	x509RootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	// Create a separate JWT key for testing
	jwtRootKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	trustDomain := "test.example.com"
	allowedClockSkew := 5 * time.Minute
	overrideTTL := 24 * time.Hour

	t.Run("with x509 only", func(t *testing.T) {
		gen := CredentialGenOptions{
			RequireX509: true,
			RequireJWT:  false,
		}

		bundle, err := GenerateBundle(x509RootKey, nil, trustDomain, allowedClockSkew, &overrideTTL, gen)
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
		gen := CredentialGenOptions{
			RequireX509: false,
			RequireJWT:  true,
		}

		bundle, err := GenerateBundle(nil, jwtRootKey, trustDomain, allowedClockSkew, &overrideTTL, gen)
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
		gen := CredentialGenOptions{
			RequireX509: true,
			RequireJWT:  true,
		}

		bundle, err := GenerateBundle(x509RootKey, jwtRootKey, trustDomain, allowedClockSkew, &overrideTTL, gen)
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

		// Verify the JWT key is the provided key
		require.Equal(t, jwtRootKey, bundle.JWTSigningKey)
	})

	t.Run("with neither x509 nor jwt", func(t *testing.T) {
		gen := CredentialGenOptions{
			RequireX509: false,
			RequireJWT:  false,
		}

		bundle, err := GenerateBundle(nil, nil, trustDomain, allowedClockSkew, &overrideTTL, gen)
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
		gen := CredentialGenOptions{
			RequireX509: true,
			RequireJWT:  true,
		}

		_, err := GenerateBundle(mockKey, jwtRootKey, trustDomain, allowedClockSkew, &overrideTTL, gen)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to sign root certificate")
	})

	t.Run("with nil TTL override", func(t *testing.T) {
		gen := CredentialGenOptions{
			RequireX509: true,
			RequireJWT:  false,
		}

		bundle, err := GenerateBundle(x509RootKey, nil, trustDomain, allowedClockSkew, nil, gen)
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
		gen := CredentialGenOptions{
			RequireX509: true,
			RequireJWT:  true,
		}

		bundle1, err := GenerateBundle(x509RootKey, jwtRootKey, trustDomain, allowedClockSkew, &overrideTTL, gen)
		require.NoError(t, err)

		// Same bundle should equal itself
		require.True(t, bundle1.Equals(bundle1))

		// Different bundle should not equal
		bundle2, err := GenerateBundle(x509RootKey, jwtRootKey, trustDomain, allowedClockSkew, &overrideTTL, gen)
		require.NoError(t, err)
		require.False(t, bundle1.Equals(bundle2))
	})
}

func TestBundleEquals(t *testing.T) {
	// Generate two different bundles for comparison
	x509Key1, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	jwtKey1, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	x509Key2, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	jwtKey2, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	// Create test bundles
	bundle1, err := GenerateBundle(x509Key1, jwtKey1, "example.com", time.Minute, nil, CredentialGenOptions{
		RequireX509: true,
		RequireJWT:  true,
	})
	require.NoError(t, err)

	bundle2, err := GenerateBundle(x509Key2, jwtKey2, "example.com", time.Minute, nil, CredentialGenOptions{
		RequireX509: true,
		RequireJWT:  true,
	})
	require.NoError(t, err)

	// Create a copy of bundle1 for testing equality with itself
	bundle1Copy := bundle1

	// Test cases
	tests := []struct {
		name     string
		bundle1  Bundle
		bundle2  Bundle
		expected bool
	}{
		{
			name:     "Same bundle should equal itself",
			bundle1:  bundle1,
			bundle2:  bundle1,
			expected: true,
		},
		{
			name:     "Copy of bundle should equal original",
			bundle1:  bundle1,
			bundle2:  bundle1Copy,
			expected: true,
		},
		{
			name:     "Different bundles should not be equal",
			bundle1:  bundle1,
			bundle2:  bundle2,
			expected: false,
		},
		{
			name:    "Bundle with different TrustAnchors should not be equal",
			bundle1: bundle1,
			bundle2: Bundle{
				TrustAnchors:     []byte("different content"),
				IssChainPEM:      bundle1.IssChainPEM,
				IssKeyPEM:        bundle1.IssKeyPEM,
				IssKey:           bundle1.IssKey,
				JWTSigningKeyPEM: bundle1.JWTSigningKeyPEM,
				JWKSJson:         bundle1.JWKSJson,
			},
			expected: false,
		},
		{
			name:    "Bundle with different IssChainPEM should not be equal",
			bundle1: bundle1,
			bundle2: Bundle{
				TrustAnchors:     bundle1.TrustAnchors,
				IssChainPEM:      []byte("different content"),
				IssKeyPEM:        bundle1.IssKeyPEM,
				IssKey:           bundle1.IssKey,
				JWTSigningKeyPEM: bundle1.JWTSigningKeyPEM,
				JWKSJson:         bundle1.JWKSJson,
			},
			expected: false,
		},
		{
			name:    "Bundle with different IssKeyPEM should not be equal",
			bundle1: bundle1,
			bundle2: Bundle{
				TrustAnchors:     bundle1.TrustAnchors,
				IssChainPEM:      bundle1.IssChainPEM,
				IssKeyPEM:        []byte("different content"),
				IssKey:           bundle1.IssKey,
				JWTSigningKeyPEM: bundle1.JWTSigningKeyPEM,
				JWKSJson:         bundle1.JWKSJson,
			},
			expected: false,
		},
		{
			name:    "Bundle with different JWTSigningKeyPEM should not be equal",
			bundle1: bundle1,
			bundle2: Bundle{
				TrustAnchors:     bundle1.TrustAnchors,
				IssChainPEM:      bundle1.IssChainPEM,
				IssKeyPEM:        bundle1.IssKeyPEM,
				IssKey:           bundle1.IssKey,
				JWTSigningKeyPEM: []byte("different content"),
				JWKSJson:         bundle1.JWKSJson,
			},
			expected: false,
		},
		{
			name:    "Bundle with different JWKSJson should not be equal",
			bundle1: bundle1,
			bundle2: Bundle{
				TrustAnchors:     bundle1.TrustAnchors,
				IssChainPEM:      bundle1.IssChainPEM,
				IssKeyPEM:        bundle1.IssKeyPEM,
				IssKey:           bundle1.IssKey,
				JWTSigningKeyPEM: bundle1.JWTSigningKeyPEM,
				JWKSJson:         []byte("different content"),
			},
			expected: false,
		},
		{
			name:     "Empty bundles should be equal",
			bundle1:  Bundle{},
			bundle2:  Bundle{},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.bundle1.Equals(tt.bundle2)
			require.Equal(t, tt.expected, result)

			// Test symmetry - equals should work both ways
			result = tt.bundle2.Equals(tt.bundle1)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestBundleMerge(t *testing.T) {
	// Generate two different bundles for testing
	x509Key1, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	jwtKey1, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	x509Key2, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	jwtKey2, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	// Create test bundles
	x509OnlyBundle, err := GenerateBundle(x509Key1, nil, "example.com", time.Minute, nil, CredentialGenOptions{
		RequireX509: true,
		RequireJWT:  false,
	})
	require.NoError(t, err)

	jwtOnlyBundle, err := GenerateBundle(nil, jwtKey1, "example.com", time.Minute, nil, CredentialGenOptions{
		RequireX509: false,
		RequireJWT:  true,
	})
	require.NoError(t, err)

	completeBundle, err := GenerateBundle(x509Key2, jwtKey2, "example.com", time.Minute, nil, CredentialGenOptions{
		RequireX509: true,
		RequireJWT:  true,
	})
	require.NoError(t, err)

	emptyBundle := Bundle{}

	// Test merging bundles in different scenarios
	t.Run("merge field by field", func(t *testing.T) {
		// Create test data with known values for all fields
		source := Bundle{
			TrustAnchors:     []byte("trust-anchors"),
			IssChainPEM:      []byte("iss-chain-pem"),
			IssKeyPEM:        []byte("iss-key-pem"),
			JWTSigningKeyPEM: []byte("jwt-key-pem"),
			JWKSJson:         []byte("jwks-json"),
			IssChain:         []*x509.Certificate{{}}, // Empty cert for testing
			IssKey:           x509Key1,
			JWTSigningKey:    jwtKey1,
		}

		// Create a JWKS for testing
		jwtKey, _ := jwk.FromRaw(jwtKey1)
		jwkSet := jwk.NewSet()
		jwkSet.AddKey(jwtKey)
		source.JWKS = jwkSet

		// Perform merge into empty bundle
		target := Bundle{}
		target.Merge(source)

		// Verify each field was merged correctly
		require.Equal(t, source.TrustAnchors, target.TrustAnchors, "TrustAnchors not merged")
		require.Equal(t, source.IssChainPEM, target.IssChainPEM, "IssChainPEM not merged")
		require.Equal(t, source.IssKeyPEM, target.IssKeyPEM, "IssKeyPEM not merged")
		require.Equal(t, source.JWTSigningKeyPEM, target.JWTSigningKeyPEM, "JWTSigningKeyPEM not merged")
		require.Equal(t, source.JWKSJson, target.JWKSJson, "JWKSJson not merged")
		require.Equal(t, source.IssChain, target.IssChain, "IssChain not merged")
		require.Equal(t, source.IssKey, target.IssKey, "IssKey not merged")
		require.Equal(t, source.JWTSigningKey, target.JWTSigningKey, "JWTSigningKey not merged")

		// We don't directly compare JWKS objects, but we can verify it exists
		// and has the same keys
		require.NotNil(t, target.JWKS)

		// Verify selective merging - only fields with values in source are merged
		partialSource := Bundle{
			TrustAnchors: []byte("new-trust-anchors"),
			IssKeyPEM:    []byte("new-iss-key-pem"),
		}

		target.Merge(partialSource)
		require.Equal(t, partialSource.TrustAnchors, target.TrustAnchors, "TrustAnchors not selectively merged")
		require.Equal(t, partialSource.IssKeyPEM, target.IssKeyPEM, "IssKeyPEM not selectively merged")
		require.Equal(t, source.IssChainPEM, target.IssChainPEM, "IssChainPEM shouldn't change")
		require.Equal(t, source.JWTSigningKeyPEM, target.JWTSigningKeyPEM, "JWTSigningKeyPEM shouldn't change")
		require.Equal(t, source.JWKSJson, target.JWKSJson, "JWKSJson shouldn't change")
	})

	t.Run("empty source doesn't change target", func(t *testing.T) {
		// Start with a non-empty target bundle
		target := completeBundle

		// Create a copy for comparison after merge
		originalTarget := completeBundle

		// Merge an empty bundle
		target.Merge(Bundle{})

		// Verify nothing changed
		require.Equal(t, originalTarget.TrustAnchors, target.TrustAnchors)
		require.Equal(t, originalTarget.IssChainPEM, target.IssChainPEM)
		require.Equal(t, originalTarget.IssKeyPEM, target.IssKeyPEM)
		require.Equal(t, originalTarget.JWTSigningKeyPEM, target.JWTSigningKeyPEM)
		require.Equal(t, originalTarget.JWKSJson, target.JWKSJson)
		require.Equal(t, originalTarget.IssChain, target.IssChain)
		require.Equal(t, originalTarget.IssKey, target.IssKey)
		require.Equal(t, originalTarget.JWTSigningKey, target.JWTSigningKey)
	})

	t.Run("merge different bundle types", func(t *testing.T) {
		// Test merging X.509 bundle into JWT-only bundle
		target := jwtOnlyBundle
		target.Merge(x509OnlyBundle)

		// Should now have both JWT and X.509 components
		require.NotEmpty(t, target.TrustAnchors, "X.509 trust anchors missing")
		require.NotEmpty(t, target.IssChainPEM, "X.509 issuer chain missing")
		require.NotEmpty(t, target.IssKeyPEM, "X.509 issuer key missing")
		require.NotEmpty(t, target.JWTSigningKeyPEM, "JWT signing key missing")
		require.NotEmpty(t, target.JWKSJson, "JWKS JSON missing")
		require.NotNil(t, target.IssChain, "IssChain missing")
		require.NotNil(t, target.IssKey, "IssKey missing")
		require.NotNil(t, target.JWTSigningKey, "JWTSigningKey missing")

		// Test merging JWT bundle into X.509-only bundle
		target = x509OnlyBundle
		target.Merge(jwtOnlyBundle)

		// Should now have both JWT and X.509 components
		require.NotEmpty(t, target.TrustAnchors, "X.509 trust anchors missing")
		require.NotEmpty(t, target.IssChainPEM, "X.509 issuer chain missing")
		require.NotEmpty(t, target.IssKeyPEM, "X.509 issuer key missing")
		require.NotEmpty(t, target.JWTSigningKeyPEM, "JWT signing key missing")
		require.NotEmpty(t, target.JWKSJson, "JWKS JSON missing")
		require.NotNil(t, target.IssChain, "IssChain missing")
		require.NotNil(t, target.IssKey, "IssKey missing")
		require.NotNil(t, target.JWTSigningKey, "JWTSigningKey missing")
	})

	t.Run("complete bundle overwrites all fields", func(t *testing.T) {
		// Start with partial bundles
		targetX509 := x509OnlyBundle
		targetJWT := jwtOnlyBundle

		// Merge complete bundle into each
		targetX509.Merge(completeBundle)
		targetJWT.Merge(completeBundle)

		// Both targets should now match the complete bundle
		require.Equal(t, completeBundle.TrustAnchors, targetX509.TrustAnchors)
		require.Equal(t, completeBundle.IssChainPEM, targetX509.IssChainPEM)
		require.Equal(t, completeBundle.IssKeyPEM, targetX509.IssKeyPEM)
		require.Equal(t, completeBundle.JWTSigningKeyPEM, targetX509.JWTSigningKeyPEM)
		require.Equal(t, completeBundle.JWKSJson, targetX509.JWKSJson)
		require.Equal(t, completeBundle.IssChain, targetX509.IssChain)
		require.Equal(t, completeBundle.IssKey, targetX509.IssKey)
		require.Equal(t, completeBundle.JWTSigningKey, targetX509.JWTSigningKey)

		require.Equal(t, completeBundle.TrustAnchors, targetJWT.TrustAnchors)
		require.Equal(t, completeBundle.IssChainPEM, targetJWT.IssChainPEM)
		require.Equal(t, completeBundle.IssKeyPEM, targetJWT.IssKeyPEM)
		require.Equal(t, completeBundle.JWTSigningKeyPEM, targetJWT.JWTSigningKeyPEM)
		require.Equal(t, completeBundle.JWKSJson, targetJWT.JWKSJson)
		require.Equal(t, completeBundle.IssChain, targetJWT.IssChain)
		require.Equal(t, completeBundle.IssKey, targetJWT.IssKey)
		require.Equal(t, completeBundle.JWTSigningKey, targetJWT.JWTSigningKey)
	})

	t.Run("complete bundle into empty bundle", func(t *testing.T) {
		target := emptyBundle
		target.Merge(completeBundle)

		require.Equal(t, completeBundle.TrustAnchors, target.TrustAnchors)
		require.Equal(t, completeBundle.IssChainPEM, target.IssChainPEM)
		require.Equal(t, completeBundle.IssKeyPEM, target.IssKeyPEM)
		require.Equal(t, completeBundle.JWTSigningKeyPEM, target.JWTSigningKeyPEM)
		require.Equal(t, completeBundle.JWKSJson, target.JWKSJson)
		require.Equal(t, completeBundle.IssChain, target.IssChain)
		require.Equal(t, completeBundle.IssKey, target.IssKey)
		require.Equal(t, completeBundle.JWTSigningKey, target.JWTSigningKey)
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
