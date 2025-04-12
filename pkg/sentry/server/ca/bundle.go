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
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
)

// Bundle is the bundle of certificates and keys used by the CA.
type Bundle struct {
	TrustAnchors []byte
	IssChainPEM  []byte
	IssKeyPEM    []byte
	IssChain     []*x509.Certificate
	IssKey       any

	JWTSigningKey    crypto.Signer
	JWTSigningKeyPEM []byte // PEM for serialization
	JWKS             []byte
}

func GenerateBundle(rootKey crypto.Signer, trustDomain string, allowedClockSkew time.Duration, overrideCATTL *time.Duration) (Bundle, error) {
	rootCert, err := generateRootCert(trustDomain, allowedClockSkew, overrideCATTL)
	if err != nil {
		return Bundle{}, fmt.Errorf("failed to generate root cert: %w", err)
	}

	rootCertDER, err := x509.CreateCertificate(rand.Reader, rootCert, rootCert, rootKey.Public(), rootKey)
	if err != nil {
		return Bundle{}, fmt.Errorf("failed to sign root certificate: %w", err)
	}
	trustAnchors := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootCertDER})

	issKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Bundle{}, err
	}
	issKeyDer, err := x509.MarshalPKCS8PrivateKey(issKey)
	if err != nil {
		return Bundle{}, err
	}
	issKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: issKeyDer})

	issCert, err := generateIssuerCert(trustDomain, allowedClockSkew, overrideCATTL)
	if err != nil {
		return Bundle{}, fmt.Errorf("failed to generate issuer cert: %w", err)
	}
	issCertDER, err := x509.CreateCertificate(rand.Reader, issCert, rootCert, &issKey.PublicKey, rootKey)
	if err != nil {
		return Bundle{}, fmt.Errorf("failed to sign issuer cert: %w", err)
	}
	issCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: issCertDER})

	issCert, err = x509.ParseCertificate(issCertDER)
	if err != nil {
		return Bundle{}, err
	}

	// Generate JWT signing key
	jwtSigningKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Bundle{}, fmt.Errorf("failed to generate JWT signing key: %w", err)
	}

	// Convert to JWK
	jwtJWK, err := jwk.FromRaw(jwtSigningKey)
	if err != nil {
		return Bundle{}, fmt.Errorf("failed to create JWK from key: %w", err)
	}

	// Set key ID and algorithm
	if err := jwtJWK.Set(jwk.KeyIDKey, "dapr-sentry-jwt-signer"); err != nil {
		return Bundle{}, fmt.Errorf("failed to set JWK kid: %w", err)
	}
	if err := jwtJWK.Set(jwk.AlgorithmKey, jwa.ES256); err != nil {
		return Bundle{}, fmt.Errorf("failed to set JWK alg: %w", err)
	}

	// Marshal the private key to PKCS8 for storage
	jwtKeyDer, err := x509.MarshalPKCS8PrivateKey(jwtSigningKey)
	if err != nil {
		return Bundle{}, fmt.Errorf("failed to marshal JWT signing key: %w", err)
	}
	jwtKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: jwtKeyDer})

	// Create a JWKS with the public key
	jwtPublicJWK, err := jwtJWK.PublicKey()
	if err != nil {
		return Bundle{}, fmt.Errorf("failed to get public JWK: %w", err)
	}

	jwkSet := jwk.NewSet()
	jwkSet.AddKey(jwtPublicJWK)

	jwksBytes, err := json.Marshal(jwkSet)
	if err != nil {
		return Bundle{}, fmt.Errorf("failed to marshal JWKS: %w", err)
	}

	return Bundle{
		TrustAnchors: trustAnchors,
		IssChainPEM:  issCertPEM,
		IssKeyPEM:    issKeyPEM,
		IssChain:     []*x509.Certificate{issCert},
		IssKey:       issKey,

		JWTSigningKey:    jwtSigningKey,
		JWTSigningKeyPEM: jwtKeyPEM,
		JWKS:             jwksBytes,
	}, nil
}
