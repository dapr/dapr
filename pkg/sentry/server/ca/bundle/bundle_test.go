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
	"crypto/elliptic"
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
		gen := MissingCredentials{
			X509: true,
			JWT:  false,
		}

		bundle, err := Generate(GenerateOptions{
			X509RootKey:        x509RootKey,
			JWTRootKey:         nil,
			TrustDomain:        trustDomain,
			AllowedClockSkew:   allowedClockSkew,
			OverrideCATTL:      &overrideTTL,
			MissingCredentials: gen,
		})
		require.NoError(t, err)

		// Verify X.509 components are present
		require.NotEmpty(t, bundle.X509.TrustAnchors)
		require.NotEmpty(t, bundle.X509.IssChainPEM)
		require.NotEmpty(t, bundle.X509.IssKeyPEM)
		require.NotNil(t, bundle.X509.IssChain)
		require.NotNil(t, bundle.X509.IssKey)

		// Verify JWT components are not present
		require.Nil(t, bundle.JWT.SigningKey)
		require.Empty(t, bundle.JWT.SigningKeyPEM)
		require.Nil(t, bundle.JWT.JWKS)
		require.Empty(t, bundle.JWT.JWKSJson)

		// Parse the generated certificates to verify them
		rootCert, _ := decodePEM(t, bundle.X509.TrustAnchors)
		require.NotNil(t, rootCert)

		// Verify TTL override was applied
		require.WithinDuration(t, time.Now().Add(overrideTTL), rootCert.NotAfter, time.Minute)
	})

	t.Run("with jwt only", func(t *testing.T) {
		gen := MissingCredentials{
			X509: false,
			JWT:  true,
		}

		bundle, err := Generate(GenerateOptions{
			X509RootKey:        nil,
			JWTRootKey:         jwtRootKey,
			TrustDomain:        trustDomain,
			AllowedClockSkew:   allowedClockSkew,
			OverrideCATTL:      &overrideTTL,
			MissingCredentials: gen,
		})
		require.NoError(t, err)

		// Verify X.509 components are not present
		require.Empty(t, bundle.X509.TrustAnchors)
		require.Empty(t, bundle.X509.IssChainPEM)
		require.Empty(t, bundle.X509.IssKeyPEM)
		require.Nil(t, bundle.X509.IssChain)
		require.Nil(t, bundle.X509.IssKey)

		// Verify JWT components are present
		require.NotNil(t, bundle.JWT.SigningKey)
		require.NotEmpty(t, bundle.JWT.SigningKeyPEM)
		require.NotNil(t, bundle.JWT.JWKS)
		require.NotEmpty(t, bundle.JWT.JWKSJson)

		// Parse JWKS to verify it
		var jwksSet map[string]interface{}
		err = json.Unmarshal(bundle.JWT.JWKSJson, &jwksSet)
		require.NoError(t, err)

		// Verify key has expected attributes
		keys, ok := jwksSet["keys"].([]interface{})
		require.True(t, ok)
		require.Len(t, keys, 1)

		k, err := jwk.FromRaw(bundle.JWT.SigningKey)
		require.NoError(t, err)
		tp, err := k.Thumbprint(DefaultKeyThumbprintAlgorithm)
		require.NoError(t, err)
		key := keys[0].(map[string]interface{})
		require.Equal(t, base64.StdEncoding.EncodeToString(tp), key["kid"])
		require.Equal(t, string(DefaultJWTSignatureAlgorithm), key["alg"])
	})

	t.Run("with both x509 and jwt", func(t *testing.T) {
		gen := MissingCredentials{
			X509: true,
			JWT:  true,
		}

		bundle, err := Generate(GenerateOptions{
			X509RootKey:        x509RootKey,
			JWTRootKey:         jwtRootKey,
			TrustDomain:        trustDomain,
			AllowedClockSkew:   allowedClockSkew,
			OverrideCATTL:      &overrideTTL,
			MissingCredentials: gen,
		})
		require.NoError(t, err)

		// Verify X.509 components are present
		require.NotEmpty(t, bundle.X509.TrustAnchors)
		require.NotEmpty(t, bundle.X509.IssChainPEM)
		require.NotEmpty(t, bundle.X509.IssKeyPEM)
		require.NotNil(t, bundle.X509.IssChain)
		require.NotNil(t, bundle.X509.IssKey)

		// Verify JWT components are present
		require.NotNil(t, bundle.JWT.SigningKey)
		require.NotEmpty(t, bundle.JWT.SigningKeyPEM)
		require.NotNil(t, bundle.JWT.JWKS)
		require.NotEmpty(t, bundle.JWT.JWKSJson)

		// Verify the JWT key is the provided key
		require.Equal(t, jwtRootKey, bundle.JWT.SigningKey)
	})

	t.Run("with neither x509 nor jwt", func(t *testing.T) {
		gen := MissingCredentials{
			X509: false,
			JWT:  false,
		}

		bundle, err := Generate(GenerateOptions{
			X509RootKey:        nil,
			JWTRootKey:         nil,
			TrustDomain:        trustDomain,
			AllowedClockSkew:   allowedClockSkew,
			OverrideCATTL:      &overrideTTL,
			MissingCredentials: gen,
		})
		require.NoError(t, err)

		// Verify bundle is empty
		require.Empty(t, bundle.X509.TrustAnchors)
		require.Empty(t, bundle.X509.IssChainPEM)
		require.Empty(t, bundle.X509.IssKeyPEM)
		require.Nil(t, bundle.X509.IssChain)
		require.Nil(t, bundle.X509.IssKey)
		require.Nil(t, bundle.JWT.SigningKey)
		require.Empty(t, bundle.JWT.SigningKeyPEM)
		require.Nil(t, bundle.JWT.JWKS)
		require.Empty(t, bundle.JWT.JWKSJson)
	})

	t.Run("with nil TTL override", func(t *testing.T) {
		gen := MissingCredentials{
			X509: true,
			JWT:  false,
		}

		bundle, err := Generate(GenerateOptions{
			X509RootKey:        x509RootKey,
			JWTRootKey:         nil,
			TrustDomain:        trustDomain,
			AllowedClockSkew:   allowedClockSkew,
			OverrideCATTL:      nil,
			MissingCredentials: gen,
		})
		require.NoError(t, err)

		// Verify X.509 components are present with default TTL
		block, _ := decodePEM(t, bundle.X509.TrustAnchors)
		rootCert, err := x509.ParseCertificate(block.Raw)
		require.NoError(t, err)

		// Default TTL is typically 1 year
		expectedDefaultTTL := time.Hour * 24 * 365
		require.WithinDuration(t, time.Now().Add(expectedDefaultTTL), rootCert.NotAfter, time.Hour*24)
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
