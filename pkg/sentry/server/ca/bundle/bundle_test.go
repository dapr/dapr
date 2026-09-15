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
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"
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

func TestRenewX509(t *testing.T) {
	_, oldRootKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	existing, err := GenerateX509(OptionsX509{
		X509RootKey:      oldRootKey,
		TrustDomain:      "test.example.com",
		AllowedClockSkew: time.Second * 20,
	})
	require.NoError(t, err)

	_, newRootKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	ttl := time.Hour * 24
	renewed, err := RenewX509(OptionsRenewX509{
		Existing:         existing,
		X509RootKey:      newRootKey,
		TrustDomain:      "test.example.com",
		AllowedClockSkew: time.Second * 20,
		OverrideCATTL:    &ttl,
	})
	require.NoError(t, err)

	t.Run("trust anchors are appended, old anchor retained byte for byte", func(t *testing.T) {
		require.Greater(t, len(renewed.TrustAnchors), len(existing.TrustAnchors))
		require.Equal(t, existing.TrustAnchors, renewed.TrustAnchors[:len(existing.TrustAnchors)])
	})

	t.Run("active issuer pair is unchanged", func(t *testing.T) {
		require.Equal(t, existing.IssChainPEM, renewed.IssChainPEM)
		require.Equal(t, existing.IssKeyPEM, renewed.IssKeyPEM)
		require.Equal(t, existing.IssChain, renewed.IssChain)
		require.Equal(t, existing.IssKey, renewed.IssKey)
	})

	t.Run("new root is self-signed with the fresh key and honours OverrideCATTL", func(t *testing.T) {
		newAnchorPEM := renewed.TrustAnchors[len(existing.TrustAnchors):]
		block, _ := pem.Decode(newAnchorPEM)
		require.NotNil(t, block)
		newRoot, cerr := x509.ParseCertificate(block.Bytes)
		require.NoError(t, cerr)
		require.NoError(t, newRoot.CheckSignatureFrom(newRoot))
		require.Equal(t, newRootKey.Public(), newRoot.PublicKey)
		require.WithinDuration(t, newRoot.NotBefore.Add(time.Second*20).Add(ttl), newRoot.NotAfter, time.Second)

		t.Run("next issuer chains to the new root only", func(t *testing.T) {
			require.Len(t, renewed.NextIssChain, 1)
			require.NoError(t, renewed.NextIssChain[0].CheckSignatureFrom(newRoot))
			require.Error(t, renewed.NextIssChain[0].CheckSignatureFrom(existing.IssChain[0]))
			oldRootBlock, _ := pem.Decode(existing.TrustAnchors)
			require.NotNil(t, oldRootBlock)
			oldRoot, perr := x509.ParseCertificate(oldRootBlock.Bytes)
			require.NoError(t, perr)
			require.Error(t, renewed.NextIssChain[0].CheckSignatureFrom(oldRoot))
		})
	})

	t.Run("next issuer key differs from the active issuer key", func(t *testing.T) {
		require.NotEqual(t, renewed.IssKeyPEM, renewed.NextIssKeyPEM)
		require.NotEmpty(t, renewed.NextIssKeyPEM)
		require.NotNil(t, renewed.NextIssKey)
	})
}
