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
	"crypto/rsa"
	"crypto/x509"
	"errors"
	"fmt"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"

	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	"github.com/dapr/kit/crypto/pem"
)

// verifyX509Bundle verifies issuer certificate key pair, and trust anchor set.
// Returns error if any of the verification fails.
// Returned CA bundle is ready for sentry.
func verifyX509Bundle(trustAnchors, issChainPEM, issKeyPEM []byte) (*bundle.X509, error) {
	trustAnchorsX509, err := pem.DecodePEMCertificates(trustAnchors)
	if err != nil {
		return nil, fmt.Errorf("failed to decode trust anchors: %w", err)
	}

	for _, cert := range trustAnchorsX509 {
		if err = cert.CheckSignatureFrom(cert); err != nil {
			return nil, fmt.Errorf("certificate in trust anchor is not self-signed: %w", err)
		}
	}

	// Strip comments from anchor certificates.
	trustAnchors = nil
	for _, cert := range trustAnchorsX509 {
		var trustAnchor []byte
		trustAnchor, err = pem.EncodeX509(cert)
		if err != nil {
			return nil, fmt.Errorf("failed to re-encode trust anchor: %w", err)
		}
		trustAnchors = append(trustAnchors, trustAnchor...)
	}

	issChain, err := pem.DecodePEMCertificatesChain(issChainPEM)
	if err != nil {
		return nil, err
	}

	// If we are using an intermediate certificate for signing, ensure we do not
	// add the root CA to the issuer chain.
	if len(issChain) > 1 {
		lastCert := issChain[len(issChain)-1]
		if err = lastCert.CheckSignatureFrom(lastCert); err == nil {
			issChain = issChain[:len(issChain)-1]
		}
	}

	// Ensure intermediate certificate is valid for signing.
	if !issChain[0].IsCA && !issChain[0].BasicConstraintsValid &&
		issChain[0].KeyUsage&x509.KeyUsageCertSign == 0 {
		return nil, errors.New("intermediate certificate is not valid for signing")
	}

	// Re-encode the issuer chain to ensure it contains only the issuer chain,
	// and strip out PEM comments since we don't want to send them to the client.
	issChainPEM, err = pem.EncodeX509Chain(issChain)
	if err != nil {
		return nil, err
	}
	if len(issChainPEM) == 0 {
		return nil, errors.New("issuer chain is empty after re-encoding")
	}

	issKey, err := pem.DecodePEMPrivateKey(issKeyPEM)
	if err != nil {
		return nil, err
	}

	issKeyPEM, err = pem.EncodePrivateKey(issKey)
	if err != nil {
		return nil, err
	}

	// Ensure issuer key matches the issuer certificate.
	ok, err := pem.PublicKeysEqual(issKey.Public(), issChain[0].PublicKey)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("issuer key does not match issuer certificate")
	}

	// Ensure issuer chain belongs to one of the trust anchors.
	trustAnchorPool := x509.NewCertPool()
	for _, cert := range trustAnchorsX509 {
		trustAnchorPool.AddCert(cert)
	}

	intPool := x509.NewCertPool()
	for _, cert := range issChain[1:] {
		intPool.AddCert(cert)
	}

	if _, err := issChain[0].Verify(x509.VerifyOptions{
		Roots:         trustAnchorPool,
		Intermediates: intPool,
	}); err != nil {
		return nil, fmt.Errorf("issuer chain does not belong to trust anchors: %w", err)
	}

	return &bundle.X509{
		TrustAnchors: trustAnchors,
		IssChainPEM:  issChainPEM,
		IssChain:     issChain,
		IssKeyPEM:    issKeyPEM,
		IssKey:       issKey,
	}, nil
}

// loadJWTSigningKey loads a JWT signing key from PEM format.
func loadJWTSigningKey(keyPEM []byte) (crypto.Signer, error) {
	privateKey, err := pem.DecodePEMPrivateKey(keyPEM)
	if err != nil {
		return nil, fmt.Errorf("failed to decode JWT signing key: %w", err)
	}

	return privateKey, nil
}

// verifyJWKS verifies that the JWKS is valid and contains a corresponding
// public key for the provided signing key. If userProvidedKeyID is provided,
// it validates that the JWKS contains a key with that exact key ID.
func verifyJWKS(jwksBytes []byte, signingKey crypto.Signer, userProvidedKeyID *string) error {
	if signingKey == nil {
		// If no signing key is provided but JWKS exists, we can't verify the match
		return errors.New("can't verify JWKS without signing key")
	}

	// Parse the JWKS
	keySet, err := jwk.Parse(jwksBytes)
	if err != nil {
		return fmt.Errorf("failed to parse JWKS: %w", err)
	}

	// Make sure the JWKS has at least one key
	if keySet.Len() == 0 {
		return errors.New("JWKS doesn't contain any keys")
	}

	// Convert signing key to JWK
	privateJWK, err := jwk.FromRaw(signingKey)
	if err != nil {
		return fmt.Errorf("failed to convert signing key to JWK: %w", err)
	}

	// Get the public key part
	publicJWK, err := privateJWK.PublicKey()
	if err != nil {
		return fmt.Errorf("failed to extract public key from JWT signing key: %w", err)
	}

	// Get the key ID if it exists on the signing key
	var signingKeyID *string
	if kid, ok := publicJWK.Get(jwk.KeyIDKey); ok {
		if s, ok := kid.(string); ok {
			signingKeyID = &s
		}
	}

	// If user provided a key ID explicitly, use that for validation
	var expectedKeyID *string
	if userProvidedKeyID != nil {
		expectedKeyID = userProvidedKeyID
	} else {
		expectedKeyID = signingKeyID
	}

	// Verify that the public key is in the JWKS
	found := false
	matchAttempted := false
	userProvidedKeyIDFound := false

	for i := range keySet.Len() {
		key, _ := keySet.Key(i)

		// If user provided a key ID, we must find a key with that exact ID and verify content matches
		if userProvidedKeyID != nil {
			if kid, ok := key.Get(jwk.KeyIDKey); ok {
				if s, ok := kid.(string); ok && s == *userProvidedKeyID {
					userProvidedKeyIDFound = true
					// Now verify that this key's content matches the signing key
					if keyMatches(key, publicJWK) {
						found = true
						break
					}
					// If we found the key ID but content doesn't match, we still need to continue
					// to check if there are other keys with the same ID that might match
				}
			}
			// Skip this key if it doesn't have the user-provided key ID
			continue
		}

		// If no user-provided key ID, check by key ID first (if both have key IDs)
		if expectedKeyID != nil {
			if kid, ok := key.Get(jwk.KeyIDKey); ok {
				if s, ok := kid.(string); ok && s == *expectedKeyID {
					found = true
					break
				}
			}
		}

		// If key ID check wasn't successful, compare the key contents
		matchAttempted = true
		if keyMatches(key, publicJWK) {
			found = true
			break
		}
	}

	if !found {
		// If a user provided a key ID but it wasn't found in the JWKS, return a specific error
		if userProvidedKeyID != nil {
			if !userProvidedKeyIDFound {
				return fmt.Errorf("JWKS doesn't contain a key with user-provided key ID %q", *userProvidedKeyID)
			} else {
				// Key ID was found but content didn't match
				return fmt.Errorf("JWKS contains a key with user-provided key ID %q but the key content doesn't match the signing key", *userProvidedKeyID)
			}
		}

		if !matchAttempted {
			var sid string
			if expectedKeyID != nil {
				sid = *expectedKeyID
			} else {
				sid = "<no-key-id>"
			}
			return fmt.Errorf("JWKS doesn't contain a key with matching key ID %q", sid)
		}
		return errors.New("JWKS doesn't contain a matching public key for the JWT signing key")
	}

	return nil
}

// keyMatches compares two JWK keys for equality
func keyMatches(key1, key2 jwk.Key) bool {
	// First, ensure we're comparing public keys
	keyPublic, err := key1.PublicKey()
	if err != nil {
		return false
	}

	// Check if the key has a valid "use" field (if present)
	if use, ok := keyPublic.Get(jwk.KeyUsageKey); ok {
		if s, ok := use.(string); ok && s != "sig" {
			// Skip keys not meant for signature verification
			return false
		}
	}

	// Get raw representations of both keys to compare
	var pubRaw any
	if err = keyPublic.Raw(&pubRaw); err != nil {
		return false
	}

	var signerPubRaw interface{}
	if err = key2.Raw(&signerPubRaw); err != nil {
		return false
	}

	// Type-specific comparisons for different key types
	switch pubKey := pubRaw.(type) {
	case *ecdsa.PublicKey:
		if signerPubKey, ok := signerPubRaw.(*ecdsa.PublicKey); ok {
			return signerPubKey.Equal(pubKey)
		}
	case *rsa.PublicKey:
		if signerPubKey, ok := signerPubRaw.(*rsa.PublicKey); ok {
			return signerPubKey.Equal(pubKey)
		}
	case ed25519.PublicKey:
		if signerPubKey, ok := signerPubRaw.(ed25519.PublicKey); ok {
			return signerPubKey.Equal(pubKey)
		}
	default:
		// If the key type is not recognized, we can't compare it
		return false
	}

	return false
}

// validateKeyAlgorithmCompatibility checks if the provided key is compatible with the specified algorithm
func validateKeyAlgorithmCompatibility(key crypto.Signer, alg jwa.SignatureAlgorithm) error {
	if key == nil {
		return errors.New("signing key is nil")
	}

	switch alg {
	case jwa.RS256, jwa.RS384, jwa.RS512, jwa.PS256, jwa.PS384, jwa.PS512:
		// RSA algorithms require an RSA key
		_, ok := key.(*rsa.PrivateKey)
		if !ok {
			return fmt.Errorf("RSA algorithm %s requires an RSA key, but got %T", alg, key)
		}
	case jwa.ES256, jwa.ES384, jwa.ES512:
		// ECDSA algorithms require an ECDSA key
		ecKey, ok := key.(*ecdsa.PrivateKey)
		if !ok {
			return fmt.Errorf("ECDSA algorithm %s requires an ECDSA key, but got %T", alg, key)
		}

		// Also validate curve size matches algorithm
		switch alg {
		case jwa.ES256:
			if ecKey.Curve != elliptic.P256() {
				return fmt.Errorf("ES256 requires a P-256 curve, got %s", ecKey.Curve.Params().Name)
			}
		case jwa.ES384:
			if ecKey.Curve != elliptic.P384() {
				return fmt.Errorf("ES384 requires a P-384 curve, got %s", ecKey.Curve.Params().Name)
			}
		case jwa.ES512:
			if ecKey.Curve != elliptic.P521() {
				return fmt.Errorf("ES512 requires a P-521 curve, got %s", ecKey.Curve.Params().Name)
			}
		}
	case jwa.EdDSA:
		// Ed25519 algorithm requires an Ed25519 key
		_, ok := key.(ed25519.PrivateKey)
		if !ok {
			return fmt.Errorf("EdDSA algorithm requires an Ed25519 key, but got %T", key)
		}
	default:
		return fmt.Errorf("unsupported signature algorithm: %s", alg)
	}

	return nil
}
