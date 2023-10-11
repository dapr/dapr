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
	"encoding/pem"
	"fmt"
	"time"
)

// Bundle is the bundle of certificates and keys used by the CA.
type Bundle struct {
	TrustAnchors []byte
	IssChainPEM  []byte
	IssKeyPEM    []byte
	IssChain     []*x509.Certificate
	IssKey       any
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

	return Bundle{
		TrustAnchors: trustAnchors,
		IssChainPEM:  issCertPEM,
		IssKeyPEM:    issKeyPEM,
		IssChain:     []*x509.Certificate{issCert},
		IssKey:       issKey,
	}, nil
}
