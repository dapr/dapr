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
	"reflect"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
)

const (
	// DefaultJWTKeyID is an identifier for the JWT signing key.
	DefaultJWTKeyID = "dapr-sentry"
	// DefaultJWTSignatureAlgorithm is set to PS256 as it is widely supported
	// by OpenID Connect providers.
	DefaultJWTSignatureAlgorithm = jwa.PS256
)

// Bundle is the bundle of certificates and keys used by the CA.
type Bundle struct {
	// TrustAnchors is the PEM encoded trust anchors.
	TrustAnchors []byte
	// IssChainPEM is the PEM encoded issuer certificate chain.
	IssChainPEM []byte
	// IssKeyPEM is the PEM encoded issuer private key.
	IssKeyPEM []byte
	// IssChain is the issuer certificate chain.
	IssChain []*x509.Certificate
	// IssKey is the issuer private key.
	IssKey any
	// JWTSigningKey is the private key used to sign JWTs.
	JWTSigningKey crypto.Signer
	// JWTSigningKeyPEM is the PEM encoded private key used to sign JWTs.
	JWTSigningKeyPEM []byte
	// JWKS is the JWK set used to verify JWTs.
	JWKS jwk.Set
	// JWKSJson is the JSON encoded JWK set used to verify JWTs.
	JWKSJson []byte
}

// Warning: this equals assumes that the serialized fields
// and their associated keys are consistent.
func (b Bundle) Equals(other Bundle) bool {
	return reflect.DeepEqual(b.TrustAnchors, other.TrustAnchors) &&
		reflect.DeepEqual(b.IssChainPEM, other.IssChainPEM) &&
		reflect.DeepEqual(b.IssKeyPEM, other.IssKeyPEM) &&
		reflect.DeepEqual(b.IssKey, other.IssKey) &&
		reflect.DeepEqual(b.JWTSigningKeyPEM, other.JWTSigningKeyPEM) &&
		reflect.DeepEqual(b.JWKSJson, other.JWKSJson)
}

func (b *Bundle) Merge(other Bundle) {
	if len(other.TrustAnchors) > 0 {
		b.TrustAnchors = other.TrustAnchors
	}
	if len(other.IssChain) > 0 {
		b.IssChain = other.IssChain
	}
	if len(other.IssChainPEM) > 0 {
		b.IssChainPEM = other.IssChainPEM
	}
	if len(other.IssKeyPEM) > 0 {
		b.IssKeyPEM = other.IssKeyPEM
	}
	if other.IssKey != nil {
		b.IssKey = other.IssKey
	}

	if len(other.JWTSigningKeyPEM) > 0 {
		b.JWTSigningKeyPEM = other.JWTSigningKeyPEM
	}
	if len(other.JWKSJson) > 0 {
		b.JWKSJson = other.JWKSJson
	}
	if other.JWTSigningKey != nil {
		b.JWTSigningKey = other.JWTSigningKey
	}
	if other.JWKS != nil {
		b.JWKS = other.JWKS
	}
}

// GenerateBundle generates the x.509 and JWT bundles if required.
func GenerateBundle(x509RootKey crypto.Signer, jwtRootKey crypto.Signer, trustDomain string, allowedClockSkew time.Duration, overrideCATTL *time.Duration, genOpts CredentialGenOptions) (Bundle, error) {
	var bundle Bundle

	if genOpts.RequireX509 {
		log.Debugf("Generating X.509 bundle with trust domain %s", trustDomain)

		rootCert, err := generateRootCert(trustDomain, allowedClockSkew, overrideCATTL)
		if err != nil {
			return Bundle{}, fmt.Errorf("failed to generate root cert: %w", err)
		}

		rootCertDER, err := x509.CreateCertificate(rand.Reader, rootCert, rootCert, x509RootKey.Public(), x509RootKey)
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
		issCertDER, err := x509.CreateCertificate(rand.Reader, issCert, rootCert, &issKey.PublicKey, x509RootKey)
		if err != nil {
			return Bundle{}, fmt.Errorf("failed to sign issuer cert: %w", err)
		}
		issCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: issCertDER})

		issCert, err = x509.ParseCertificate(issCertDER)
		if err != nil {
			return Bundle{}, err
		}

		bundle.TrustAnchors = trustAnchors
		bundle.IssChainPEM = issCertPEM
		bundle.IssKeyPEM = issKeyPEM
		bundle.IssChain = []*x509.Certificate{issCert}
		bundle.IssKey = issKey
	}

	if genOpts.RequireJWT {
		log.Debugf("Generating JWT bundle with trust domain %s", trustDomain)

		jwtKey, err := jwk.FromRaw(jwtRootKey)
		if err != nil {
			return Bundle{}, fmt.Errorf("failed to create JWK from key: %w", err)
		}

		// Marshal the private key to PKCS8 for storage
		jwtKeyDer, err := x509.MarshalPKCS8PrivateKey(jwtRootKey)
		if err != nil {
			return Bundle{}, fmt.Errorf("failed to marshal JWT signing key: %w", err)
		}
		jwtKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: jwtKeyDer})

		// Create a JWKS with the public key
		jwtPublicJWK, err := jwtKey.PublicKey()
		if err != nil {
			return Bundle{}, fmt.Errorf("failed to get public JWK: %w", err)
		}

		jwtPublicJWK.Set(jwk.KeyIDKey, DefaultJWTKeyID)
		jwtPublicJWK.Set(jwk.AlgorithmKey, DefaultJWTSignatureAlgorithm)
		jwtPublicJWK.Set(jwk.KeyUsageKey, "sig")

		// TODO: consider setting x5c, x5t, and x5t#S256

		jwkSet := jwk.NewSet()
		jwkSet.AddKey(jwtPublicJWK)

		jwksJson, err := json.Marshal(jwkSet)
		if err != nil {
			return Bundle{}, fmt.Errorf("failed to marshal JWKS: %w", err)
		}

		bundle.JWTSigningKey = jwtRootKey
		bundle.JWTSigningKeyPEM = jwtKeyPEM
		bundle.JWKS = jwkSet
		bundle.JWKSJson = jwksJson
	}

	return bundle, nil
}
