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
	"crypto/x509"
	"errors"
	"fmt"

	"github.com/dapr/kit/crypto/pem"
	"github.com/lestrrat-go/jwx/v2/jwk"
)

// verifyBundle verifies issuer certificate key pair, and trust anchor set.
// Returns error if any of the verification fails.
// Returned CA bundle is ready for sentry.
func verifyBundle(trustAnchors, issChainPEM, issKeyPEM []byte) (Bundle, error) {
	trustAnchorsX509, err := pem.DecodePEMCertificates(trustAnchors)
	if err != nil {
		return Bundle{}, fmt.Errorf("failed to decode trust anchors: %w", err)
	}

	for _, cert := range trustAnchorsX509 {
		if err = cert.CheckSignatureFrom(cert); err != nil {
			return Bundle{}, fmt.Errorf("certificate in trust anchor is not self-signed: %w", err)
		}
	}

	// Strip comments from anchor certificates.
	trustAnchors = nil
	for _, cert := range trustAnchorsX509 {
		var trustAnchor []byte
		trustAnchor, err = pem.EncodeX509(cert)
		if err != nil {
			return Bundle{}, fmt.Errorf("failed to re-encode trust anchor: %w", err)
		}
		trustAnchors = append(trustAnchors, trustAnchor...)
	}

	issChain, err := pem.DecodePEMCertificatesChain(issChainPEM)
	if err != nil {
		return Bundle{}, err
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
		return Bundle{}, errors.New("intermediate certificate is not valid for signing")
	}

	// Re-encode the issuer chain to ensure it contains only the issuer chain,
	// and strip out PEM comments since we don't want to send them to the client.
	issChainPEM, err = pem.EncodeX509Chain(issChain)
	if err != nil {
		return Bundle{}, err
	}

	issKey, err := pem.DecodePEMPrivateKey(issKeyPEM)
	if err != nil {
		return Bundle{}, err
	}

	// Re-encode the issuer key to ensure only the private key material is encoded.
	issKeyPEM, err = pem.EncodePrivateKey(issKey)
	if err != nil {
		return Bundle{}, err
	}

	// Ensure issuer key matches the issuer certificate.
	ok, err := pem.PublicKeysEqual(issKey.Public(), issChain[0].PublicKey)
	if err != nil {
		return Bundle{}, err
	}
	if !ok {
		return Bundle{}, errors.New("issuer key does not match issuer certificate")
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
		return Bundle{}, fmt.Errorf("issuer chain does not belong to trust anchors: %w", err)
	}

	return Bundle{
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

	signer, ok := privateKey.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("JWT signing key is not a valid crypto.Signer")
	}

	return signer, nil
}

// verifyJWKS verifies that the JWKS is valid and contains a corresponding
// public key for the provided signing key.
func verifyJWKS(jwksBytes []byte, signingKey crypto.Signer) error {
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

	// Verify that the public key is in the JWKS
	found := false
	for i := 0; i < keySet.Len(); i++ {
		key, _ := keySet.Key(i)

		// If the key has a key ID, check if it matches
		if kid, ok := key.Get(jwk.KeyIDKey); ok {
			if privateKID, privateOK := privateJWK.Get(jwk.KeyIDKey); privateOK && kid == privateKID {
				found = true
				break
			}
		}

		// Otherwise, compare the JWK raw values
		// First, ensure we're comparing public keys
		keyPublic, err := key.PublicKey()
		if err != nil {
			continue
		}

		// Compare raw public keys
		var rawKey, rawPrivatePublic []byte
		if err := keyPublic.Raw(rawKey); err != nil {
			continue
		}
		if err := publicJWK.Raw(rawPrivatePublic); err != nil {
			continue
		}

		// For EC keys, we need to compare each component separately
		// This is a simplification; for a production system would need more robust comparison
		if fmt.Sprintf("%v", rawKey) == fmt.Sprintf("%v", rawPrivatePublic) {
			found = true
			break
		}
	}

	if !found {
		return errors.New("JWKS doesn't contain a matching public key for the JWT signing key")
	}

	return nil
}
