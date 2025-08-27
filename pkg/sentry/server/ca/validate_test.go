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
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
)

func genCrt(t *testing.T,
	name string,
	signCrt *x509.Certificate,
	signKey crypto.Signer,
) ([]byte, *x509.Certificate, []byte, crypto.Signer) {
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	require.NoError(t, err)

	tmpl := x509.Certificate{
		Subject:               pkix.Name{Organization: []string{name}},
		SerialNumber:          serialNumber,
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
	}

	pk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	if signCrt == nil {
		signCrt = &tmpl
	}
	if signKey == nil {
		signKey = pk
	}

	crtDER, err := x509.CreateCertificate(rand.Reader, &tmpl, signCrt, pk.Public(), signKey)
	require.NoError(t, err)

	crt, err := x509.ParseCertificate(crtDER)
	require.NoError(t, err)
	crtPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: crtDER})

	pkDER, err := x509.MarshalPKCS8PrivateKey(pk)
	require.NoError(t, err)
	pkPEM := pem.EncodeToMemory(&pem.Block{
		Type: "PRIVATE KEY", Bytes: pkDER,
	})

	return crtPEM, crt, pkPEM, pk
}

func joinPEM(crts ...[]byte) []byte {
	var b []byte
	for _, crt := range crts {
		b = append(b, crt...)
	}
	return b
}

func TestVerifyBundle(t *testing.T) {
	rootPEM, rootCrt, _, rootPK := genCrt(t, "root", nil, nil)
	//nolint:dogsled
	rootBPEM, _, _, _ := genCrt(t, "rootB", nil, nil)
	int1PEM, int1Crt, int1PKPEM, int1PK := genCrt(t, "int1", rootCrt, rootPK)
	int2PEM, int2Crt, int2PKPEM, int2PK := genCrt(t, "int2", int1Crt, int1PK)

	tests := map[string]struct {
		issChainPEM []byte
		issKeyPEM   []byte
		trustBundle []byte
		expErr      bool
		expBundle   *bundle.X509
	}{
		"if issuer chain pem empty, expect error": {
			issChainPEM: nil,
			issKeyPEM:   int1PKPEM,
			trustBundle: rootPEM,
			expErr:      true,
			expBundle:   nil,
		},
		"if issuer key pem empty, expect error": {
			issChainPEM: int1PEM,
			issKeyPEM:   nil,
			trustBundle: rootPEM,
			expErr:      true,
			expBundle:   nil,
		},
		"if issuer trust bundle pem empty, expect error": {
			issChainPEM: int1PEM,
			issKeyPEM:   int1PKPEM,
			trustBundle: nil,
			expErr:      true,
			expBundle:   nil,
		},
		"invalid issuer chain PEM should error": {
			issChainPEM: []byte("invalid"),
			issKeyPEM:   int1PKPEM,
			trustBundle: rootPEM,
			expErr:      true,
			expBundle:   nil,
		},
		"invalid issuer key PEM should error": {
			issChainPEM: int1PEM,
			issKeyPEM:   []byte("invalid"),
			trustBundle: rootPEM,
			expErr:      true,
			expBundle:   nil,
		},
		"invalid trust bundle PEM should error": {
			issChainPEM: int1PEM,
			issKeyPEM:   int1PKPEM,
			trustBundle: []byte("invalid"),
			expErr:      true,
			expBundle:   nil,
		},
		"if issuer chain is in wrong order, expect error": {
			issChainPEM: joinPEM(int1PEM, int2PEM),
			issKeyPEM:   int2PKPEM,
			trustBundle: joinPEM(rootPEM, rootBPEM),
			expErr:      true,
			expBundle:   nil,
		},
		"if issuer key does not belong to issuer certificate, expect error": {
			issChainPEM: joinPEM(int2PEM, int1PEM),
			issKeyPEM:   int1PKPEM,
			trustBundle: joinPEM(rootPEM, rootBPEM),
			expErr:      true,
			expBundle:   nil,
		},
		"if trust anchors contains non root certificates, exp error": {
			issChainPEM: joinPEM(int2PEM, int1PEM),
			issKeyPEM:   int2PKPEM,
			trustBundle: joinPEM(rootPEM, rootBPEM, int1PEM),
			expErr:      true,
			expBundle:   nil,
		},
		"if issuer chain doesn't belong to trust anchors, expect error": {
			issChainPEM: joinPEM(int2PEM, int1PEM),
			issKeyPEM:   int2PKPEM,
			trustBundle: joinPEM(rootBPEM),
			expErr:      true,
			expBundle:   nil,
		},
		"valid chain should not error": {
			issChainPEM: int1PEM,
			issKeyPEM:   int1PKPEM,
			trustBundle: rootPEM,
			expErr:      false,
			expBundle: &bundle.X509{
				TrustAnchors: rootPEM,
				IssChainPEM:  joinPEM(int1PEM),
				IssKeyPEM:    int1PKPEM,
				IssChain:     []*x509.Certificate{int1Crt},
				IssKey:       int1PK,
			},
		},
		"valid long chain should not error": {
			issChainPEM: joinPEM(int2PEM, int1PEM),
			issKeyPEM:   int2PKPEM,
			trustBundle: joinPEM(rootPEM, rootBPEM),
			expErr:      false,
			expBundle: &bundle.X509{
				TrustAnchors: joinPEM(rootPEM, rootBPEM),
				IssChainPEM:  joinPEM(int2PEM, int1PEM),
				IssKeyPEM:    int2PKPEM,
				IssChain:     []*x509.Certificate{int2Crt, int1Crt},
				IssKey:       int2PK,
			},
		},
		"is root certificate in chain, expect to be removed": {
			issChainPEM: joinPEM(int2PEM, int1PEM, rootPEM),
			issKeyPEM:   int2PKPEM,
			trustBundle: joinPEM(rootPEM, rootBPEM),
			expErr:      false,
			expBundle: &bundle.X509{
				TrustAnchors: joinPEM(rootPEM, rootBPEM),
				IssChainPEM:  joinPEM(int2PEM, int1PEM),
				IssKeyPEM:    int2PKPEM,
				IssChain:     []*x509.Certificate{int2Crt, int1Crt},
				IssKey:       int2PK,
			},
		},
		"comments are removed from parsed issuer chain, private key and trust anchors": {
			issChainPEM: joinPEM(
				[]byte("# this is a comment\n"),
				int2PEM,
				[]byte("# this is a comment\n"),
				int1PEM,
				[]byte("# this is a comment\n"),
			),
			issKeyPEM: joinPEM(
				[]byte("# this is a comment\n"),
				int2PKPEM,
				[]byte("# this is a comment\n"),
			),
			trustBundle: joinPEM(
				[]byte("# this is a comment\n"),
				rootPEM,
				[]byte("# this is a comment\n"),
				rootBPEM,
				[]byte("# this is a comment\n"),
			),
			expErr: false,
			expBundle: &bundle.X509{
				TrustAnchors: joinPEM(rootPEM, rootBPEM),
				IssChainPEM:  joinPEM(int2PEM, int1PEM),
				IssKeyPEM:    int2PKPEM,
				IssChain:     []*x509.Certificate{int2Crt, int1Crt},
				IssKey:       int2PK,
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			Bundle, err := verifyX509Bundle(test.trustBundle, test.issChainPEM, test.issKeyPEM)
			assert.Equal(t, test.expErr, err != nil, "%v", err)
			require.Equal(t, test.expBundle, Bundle)
		})
	}
}

// TestVerifyJWKS tests the verifyJWKS function which validates that a JWKS contains
// a matching public key for a given signing key.
func TestVerifyJWKS(t *testing.T) {
	// Generate a test signing key
	signingKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err, "Failed to generate signing key")

	// Create and set up test cases
	t.Run("valid JWKS with matching key", func(t *testing.T) {
		// Create a JWKS with the public key of the signing key
		jwksBytes := createJWKS(t, signingKey, "test-key")

		// Verify the JWKS
		err := verifyJWKS(jwksBytes, signingKey, nil)
		assert.NoError(t, err, "JWKS verification should succeed with valid key")
	})

	t.Run("valid JWKS with multiple keys including the matching one", func(t *testing.T) {
		// Generate an additional key
		extraKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err, "Failed to generate extra key")

		// Create a JWKS with multiple keys
		jwksBytes := createMultiKeyJWKS(t, map[string]crypto.Signer{
			"extra-key": extraKey,
			"test-key":  signingKey,
		})

		// Verify the JWKS
		err = verifyJWKS(jwksBytes, signingKey, nil)
		assert.NoError(t, err, "JWKS verification should succeed when multiple keys are present")
	})

	t.Run("nil signing key", func(t *testing.T) {
		jwksBytes := createJWKS(t, signingKey, "test-key")
		err := verifyJWKS(jwksBytes, nil, nil)
		require.Error(t, err, "JWKS verification should fail with nil signing key")
		assert.Contains(t, err.Error(), "can't verify JWKS without signing key")
	})

	t.Run("invalid JWKS format", func(t *testing.T) {
		invalidJWKS := []byte(`{"keys": [{"invalid": "format"]}`)
		err := verifyJWKS(invalidJWKS, signingKey, nil)
		require.Error(t, err, "JWKS verification should fail with invalid JWKS format")
		assert.Contains(t, err.Error(), "failed to parse JWKS")
	})

	t.Run("empty JWKS", func(t *testing.T) {
		emptyJWKS := []byte(`{"keys": []}`)
		err := verifyJWKS(emptyJWKS, signingKey, nil)
		require.Error(t, err, "JWKS verification should fail with empty JWKS")
		assert.Contains(t, err.Error(), "JWKS doesn't contain any keys")
	})

	t.Run("JWKS without matching key", func(t *testing.T) {
		// Generate a different key
		differentKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err, "Failed to generate different key")

		// Create a JWKS with a different key
		jwksBytes := createJWKS(t, differentKey, "different-key")

		// Verify the JWKS with the original signing key
		err = verifyJWKS(jwksBytes, signingKey, nil)
		require.Error(t, err, "JWKS verification should fail when no matching key is found")
		assert.Contains(t, err.Error(), "JWKS doesn't contain a matching public key")
	})

	t.Run("match key by keyID", func(t *testing.T) {
		// Create a signing key with a specific key ID
		privateJWK, err := jwk.FromRaw(signingKey)
		require.NoError(t, err, "Failed to convert signing key to JWK")

		err = privateJWK.Set(jwk.KeyIDKey, "specific-kid")
		require.NoError(t, err, "Failed to set key ID")

		// Create a JWKS with a key that has the same ID
		key, err := jwk.FromRaw(signingKey.Public())
		require.NoError(t, err, "Failed to create public key")

		err = key.Set(jwk.KeyIDKey, "specific-kid")
		require.NoError(t, err, "Failed to set key ID")

		err = key.Set(jwk.AlgorithmKey, "ES256")
		require.NoError(t, err, "Failed to set algorithm")

		err = key.Set(jwk.KeyUsageKey, "sig")
		require.NoError(t, err, "Failed to set key usage")

		keySet := jwk.NewSet()
		keySet.AddKey(key)

		jwksBytes, err := json.Marshal(keySet)
		require.NoError(t, err, "Failed to marshal JWKS")

		// Verification should succeed because the key IDs match
		err = verifyJWKS(jwksBytes, signingKey, nil)
		assert.NoError(t, err, "JWKS verification should succeed when key IDs match")
	})

	t.Run("large JWKS with many keys", func(t *testing.T) {
		// Create a large JWKS with many keys
		const numKeys = 50
		keys := make(map[string]crypto.Signer, numKeys)

		// Generate many different keys
		for i := range numKeys - 1 {
			key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			require.NoError(t, err, "Failed to generate key")
			keys[fmt.Sprintf("key-%d", i)] = key
		}

		// Add the real signing key as the last one
		keys["real-key"] = signingKey

		jwksBytes := createMultiKeyJWKS(t, keys)

		// Verification should succeed even with many keys
		err := verifyJWKS(jwksBytes, signingKey, nil)
		assert.NoError(t, err, "JWKS verification should succeed even with many keys")
	})

	t.Run("malformed key in JWKS", func(t *testing.T) {
		// Create a JWKS with a valid structure but malformed key data
		malformedJWKS := []byte(`{
			"keys": [
				{
					"kty": "EC",
					"use": "sig", 
					"kid": "test-key",
					"crv": "P-256",
					"x": "invalidbase64",
					"y": "alsoInvalidbase64"
				}
			]
		}`)

		err := verifyJWKS(malformedJWKS, signingKey, nil)
		assert.Error(t, err, "JWKS verification should fail with malformed key")
	})

	t.Run("mixed key types in JWKS", func(t *testing.T) {
		// Create a JWKS with mixed key types (EC and RSA)
		mixedJWKS := []byte(`{
			"keys": [
				{
					"kty": "RSA",
					"use": "sig",
					"kid": "rsa-key",
					"n": "someRSAModulus",
					"e": "AQAB"
				},
				{
					"kty": "EC",
					"use": "sig",
					"kid": "ec-key",
					"crv": "P-256",
					"x": "someECPointX",
					"y": "someECPointY"
				}
			]
		}`)

		err := verifyJWKS(mixedJWKS, signingKey, nil)
		require.Error(t, err, "JWKS verification should fail when no matching key is found")
		assert.Contains(t, err.Error(), "JWKS doesn't contain a matching public key")
	})

	t.Run("user-provided key ID matches JWKS", func(t *testing.T) {
		// Create a JWKS with a specific key ID
		jwksBytes := createJWKS(t, signingKey, "user-specified-key-id")

		// Verify the JWKS with the user-provided key ID
		userKeyID := "user-specified-key-id"
		err := verifyJWKS(jwksBytes, signingKey, &userKeyID)
		assert.NoError(t, err, "JWKS verification should succeed when user-provided key ID matches")
	})

	t.Run("user-provided key ID not found in JWKS", func(t *testing.T) {
		// Create a JWKS with a different key ID
		jwksBytes := createJWKS(t, signingKey, "actual-key-id")

		// Verify the JWKS with a different user-provided key ID
		userKeyID := "missing-key-id"
		err := verifyJWKS(jwksBytes, signingKey, &userKeyID)
		require.Error(t, err, "JWKS verification should fail when user-provided key ID is not found")
		assert.Contains(t, err.Error(), "JWKS doesn't contain a key with user-provided key ID \"missing-key-id\"")
	})

	t.Run("user-provided key ID with multiple keys in JWKS", func(t *testing.T) {
		// Create a JWKS with multiple keys, including the user-specified one
		extraKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err, "Failed to generate extra key")

		keys := map[string]crypto.Signer{
			"extra-key-1":        extraKey,
			"user-specified-key": signingKey,
			"extra-key-2":        extraKey,
		}
		jwksBytes := createMultiKeyJWKS(t, keys)

		// Verify the JWKS with the user-provided key ID
		userKeyID := "user-specified-key"
		err = verifyJWKS(jwksBytes, signingKey, &userKeyID)
		assert.NoError(t, err, "JWKS verification should succeed when user-provided key ID is found among multiple keys")
	})

	t.Run("user-provided key ID matches but content differs", func(t *testing.T) {
		// Generate a different key with same key ID as user specified
		differentKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err, "Failed to generate different key")

		// Create a JWKS with the different key but same key ID as user-provided
		jwksBytes := createJWKS(t, differentKey, "same-key-id")

		// Verify the JWKS with user-provided key ID - should fail because key content doesn't match
		userKeyID := "same-key-id"
		err = verifyJWKS(jwksBytes, signingKey, &userKeyID)
		require.Error(t, err, "JWKS verification should fail when user-provided key ID matches but key content differs")
		// The key ID exists in JWKS but content doesn't match signing key, so verification should fail
		assert.Contains(t, err.Error(), "JWKS contains a key with user-provided key ID \"same-key-id\" but the key content doesn't match")
	})
}

// Helper function to create a JWKS from a signing key
func createJWKS(t *testing.T, signingKey crypto.Signer, keyID string) []byte {
	// Convert the key to a JWK
	key, err := jwk.FromRaw(signingKey.Public())
	require.NoError(t, err, "Failed to convert public key to JWK")

	// Set the key ID
	err = key.Set(jwk.KeyIDKey, keyID)
	require.NoError(t, err, "Failed to set key ID")

	// Set the algorithm
	err = key.Set(jwk.AlgorithmKey, "ES256")
	require.NoError(t, err, "Failed to set algorithm")

	// Set the key use
	err = key.Set(jwk.KeyUsageKey, "sig")
	require.NoError(t, err, "Failed to set key usage")

	// Create a JWKS
	keySet := jwk.NewSet()
	keySet.AddKey(key)

	// Marshal the JWKS to JSON
	jwksBytes, err := json.Marshal(keySet)
	require.NoError(t, err, "Failed to marshal JWKS to JSON")

	return jwksBytes
}

// Helper function to create a JWKS with multiple keys
func createMultiKeyJWKS(t *testing.T, keys map[string]crypto.Signer) []byte {
	keySet := jwk.NewSet()

	for keyID, signingKey := range keys {
		// Convert the key to a JWK
		key, err := jwk.FromRaw(signingKey.Public())
		require.NoError(t, err, "Failed to convert public key to JWK")

		// Set the key ID
		err = key.Set(jwk.KeyIDKey, keyID)
		require.NoError(t, err, "Failed to set key ID")

		// Set the algorithm
		err = key.Set(jwk.AlgorithmKey, "ES256")
		require.NoError(t, err, "Failed to set algorithm")

		// Set the key use
		err = key.Set(jwk.KeyUsageKey, "sig")
		require.NoError(t, err, "Failed to set key usage")

		// Add the key to the set
		keySet.AddKey(key)
	}

	// Marshal the JWKS to JSON
	jwksBytes, err := json.Marshal(keySet)
	require.NoError(t, err, "Failed to marshal JWKS to JSON")

	return jwksBytes
}

func TestValidateKeyAlgorithmCompatibility(t *testing.T) {
	// Generate test keys
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	ecdsaP256Key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	ecdsaP384Key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	require.NoError(t, err)

	ecdsaP521Key, err := ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
	require.NoError(t, err)

	// Generate Ed25519 key pair
	edPublicKey, edPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	_ = edPublicKey // Avoid unused variable warning

	tests := []struct {
		name      string
		key       crypto.Signer
		algorithm jwa.SignatureAlgorithm
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "nil key",
			key:       nil,
			algorithm: jwa.RS256,
			wantErr:   true,
			errMsg:    "signing key is nil",
		},
		{
			name:      "RSA key with RS256",
			key:       rsaKey,
			algorithm: jwa.RS256,
			wantErr:   false,
		},
		{
			name:      "RSA key with PS256",
			key:       rsaKey,
			algorithm: jwa.PS256,
			wantErr:   false,
		},
		{
			name:      "RSA key with ES256",
			key:       rsaKey,
			algorithm: jwa.ES256,
			wantErr:   true,
			errMsg:    "ECDSA algorithm ES256 requires an ECDSA key",
		},
		{
			name:      "ECDSA P-256 key with ES256",
			key:       ecdsaP256Key,
			algorithm: jwa.ES256,
			wantErr:   false,
		},
		{
			name:      "ECDSA P-384 key with ES384",
			key:       ecdsaP384Key,
			algorithm: jwa.ES384,
			wantErr:   false,
		},
		{
			name:      "ECDSA P-521 key with ES512",
			key:       ecdsaP521Key,
			algorithm: jwa.ES512,
			wantErr:   false,
		},
		{
			name:      "ECDSA P-256 key with ES384",
			key:       ecdsaP256Key,
			algorithm: jwa.ES384,
			wantErr:   true,
			errMsg:    "ES384 requires a P-384 curve",
		},
		{
			name:      "ECDSA P-256 key with RS256",
			key:       ecdsaP256Key,
			algorithm: jwa.RS256,
			wantErr:   true,
			errMsg:    "RSA algorithm RS256 requires an RSA key",
		},
		{
			name:      "Ed25519 key with EdDSA",
			key:       edPrivateKey,
			algorithm: jwa.EdDSA,
			wantErr:   false,
		},
		{
			name:      "Ed25519 key with ES256",
			key:       edPrivateKey,
			algorithm: jwa.ES256,
			wantErr:   true,
			errMsg:    "ECDSA algorithm ES256 requires an ECDSA key",
		},
		{
			name:      "Ed25519 key with RS256",
			key:       edPrivateKey,
			algorithm: jwa.RS256,
			wantErr:   true,
			errMsg:    "RSA algorithm RS256 requires an RSA key",
		},
		{
			name:      "RSA key with EdDSA",
			key:       rsaKey,
			algorithm: jwa.EdDSA,
			wantErr:   true,
			errMsg:    "EdDSA algorithm requires an Ed25519 key",
		},
		{
			name:      "ECDSA key with EdDSA",
			key:       ecdsaP256Key,
			algorithm: jwa.EdDSA,
			wantErr:   true,
			errMsg:    "EdDSA algorithm requires an Ed25519 key",
		},
		{
			name:      "Unsupported algorithm",
			key:       rsaKey,
			algorithm: "unsupported",
			wantErr:   true,
			errMsg:    "unsupported signature algorithm",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateKeyAlgorithmCompatibility(tt.key, tt.algorithm)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}
