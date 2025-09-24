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

package bundle

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
)

const (
	// DefaultKeyThumbprintAlgorithm
	DefaultKeyThumbprintAlgorithm = crypto.SHA256
	// DefaultJWTSignatureAlgorithm is set to RS256 by default as it is the most compatible algorithm.
	DefaultJWTSignatureAlgorithm = jwa.RS256
)

// Bundle is the bundle of certificates and keys used by the CA.
type Bundle struct {
	X509 *X509
	JWT  *JWT
}

type OptionsX509 struct {
	X509RootKey      crypto.Signer
	TrustDomain      string
	AllowedClockSkew time.Duration
	OverrideCATTL    *time.Duration // Optional override for CA TTL
}

type OptionsJWT struct {
	TrustDomain string
	JWTRootKey  crypto.Signer
}

type X509 struct {
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
}

type JWT struct {
	// SigningKey is the private key used to sign JWTs.
	SigningKey crypto.Signer
	// SigningKeyPEM is the PEM encoded private key used to sign JWTs.
	SigningKeyPEM []byte
	// JWKS is the JWK set used to verify JWTs.
	JWKS jwk.Set
	// JWKSJson is the JSON encoded JWK set used to verify JWTs.
	JWKSJson []byte
}

func GenerateX509(opts OptionsX509) (*X509, error) {
	log.Debugf("Generating X.509 bundle with trust domain %s", opts.TrustDomain)

	rootCert, err := generateRootCert(opts.TrustDomain, opts.AllowedClockSkew, opts.OverrideCATTL)
	if err != nil {
		return nil, fmt.Errorf("failed to generate root cert: %w", err)
	}

	rootCertDER, err := x509.CreateCertificate(rand.Reader, rootCert, rootCert, opts.X509RootKey.Public(), opts.X509RootKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign root certificate: %w", err)
	}
	trustAnchors := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootCertDER})

	issKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	issKeyDer, err := x509.MarshalPKCS8PrivateKey(issKey)
	if err != nil {
		return nil, err
	}
	issKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: issKeyDer})

	issCert, err := generateIssuerCert(opts.TrustDomain, opts.AllowedClockSkew, opts.OverrideCATTL)
	if err != nil {
		return nil, fmt.Errorf("failed to generate issuer cert: %w", err)
	}
	issCertDER, err := x509.CreateCertificate(rand.Reader, issCert, rootCert, &issKey.PublicKey, opts.X509RootKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign issuer cert: %w", err)
	}
	issCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: issCertDER})

	issCert, err = x509.ParseCertificate(issCertDER)
	if err != nil {
		return nil, err
	}

	return &X509{
		TrustAnchors: trustAnchors,
		IssChainPEM:  issCertPEM,
		IssKeyPEM:    issKeyPEM,
		IssChain:     []*x509.Certificate{issCert},
		IssKey:       issKey,
	}, nil
}

func GenerateJWT(opts OptionsJWT) (*JWT, error) {
	log.Debugf("Generating JWT bundle with trust domain %s", opts.TrustDomain)

	jwtKey, err := jwk.FromRaw(opts.JWTRootKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create JWK from key: %w", err)
	}

	// Marshal the private key to PKCS8 for storage
	jwtKeyDer, err := x509.MarshalPKCS8PrivateKey(opts.JWTRootKey)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal JWT signing key: %w", err)
	}
	jwtKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: jwtKeyDer})

	// Create a JWKS with the public key
	jwtPublicJWK, err := jwtKey.PublicKey()
	if err != nil {
		return nil, fmt.Errorf("failed to get public JWK: %w", err)
	}

	// Use the sha256 thumbprint as the key ID
	thumbprint, err := jwtKey.Thumbprint(DefaultKeyThumbprintAlgorithm)
	if err != nil {
		return nil, fmt.Errorf("failed to generate JWK thumbprint: %w", err)
	}

	jwtPublicJWK.Set(jwk.KeyIDKey, base64.StdEncoding.EncodeToString(thumbprint))
	jwtPublicJWK.Set(jwk.AlgorithmKey, DefaultJWTSignatureAlgorithm)
	jwtPublicJWK.Set(jwk.KeyUsageKey, "sig")

	// TODO(@jjcollinge): consider setting x5c, x5t, and x5t#S256

	jwkSet := jwk.NewSet()
	jwkSet.AddKey(jwtPublicJWK)

	jwksJSON, err := json.Marshal(jwkSet)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal JWKS: %w", err)
	}

	return &JWT{
		SigningKey:    opts.JWTRootKey,
		SigningKeyPEM: jwtKeyPEM,
		JWKS:          jwkSet,
		JWKSJson:      jwksJSON,
	}, nil
}
