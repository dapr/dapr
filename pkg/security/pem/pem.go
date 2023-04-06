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

package pem

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
)

// DecodePEMCertificatesChain takes a PEM-encoded x509 certificates byte array
// and returns all certificates in a slice of x509.Certificate objects.
// Expects certificates to be a chain with leaf certificate to be first in the
// byte array.
func DecodePEMCertificatesChain(crtb []byte) ([]*x509.Certificate, error) {
	certs, err := DecodePEMCertificates(crtb)
	if err != nil {
		return nil, err
	}

	for i := 0; i < len(certs)-1; i++ {
		if certs[i].CheckSignatureFrom(certs[i+1]) != nil {
			return nil, errors.New("certificate chain is not valid")
		}
	}

	return certs, nil
}

// DecodePEMCertificatesChain takes a PEM-encoded x509 certificates byte array
// and returns all certificates in a slice of x509.Certificate objects.
func DecodePEMCertificates(crtb []byte) ([]*x509.Certificate, error) {
	certs := []*x509.Certificate{}
	for len(crtb) > 0 {
		var err error
		var cert *x509.Certificate

		cert, crtb, err = decodeCertificatePEM(crtb)
		if err != nil {
			return nil, err
		}
		if cert != nil {
			// it's a cert, add to pool
			certs = append(certs, cert)
		}
	}

	if len(certs) == 0 {
		return nil, errors.New("no certificates found")
	}

	return certs, nil
}

func decodeCertificatePEM(crtb []byte) (*x509.Certificate, []byte, error) {
	block, crtb := pem.Decode(crtb)
	if block == nil {
		return nil, nil, nil
	}
	if block.Type != "CERTIFICATE" {
		return nil, nil, nil
	}
	c, err := x509.ParseCertificate(block.Bytes)
	return c, crtb, err
}

// DecodePEMPrivateKey takes a key PEM byte array and returns an object that
// represents either an RSA or EC private key.
func DecodePEMPrivateKey(key []byte) (crypto.Signer, error) {
	block, _ := pem.Decode(key)
	if block == nil {
		return nil, errors.New("key is not PEM encoded")
	}

	switch block.Type {
	case "EC PRIVATE KEY":
		return x509.ParseECPrivateKey(block.Bytes)
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		return key.(crypto.Signer), nil
	default:
		return nil, fmt.Errorf("unsupported block type %s", block.Type)
	}
}

// EncodePrivateKey will encode a private key into PEM format.
func EncodePrivateKey(key any) ([]byte, error) {
	var (
		keyBytes  []byte
		err       error
		blockType string
	)

	switch key := key.(type) {
	case *ecdsa.PrivateKey, *ed25519.PrivateKey:
		keyBytes, err = x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return nil, err
		}
		blockType = "PRIVATE KEY"
	default:
		return nil, fmt.Errorf("unsupported key type %T", key)
	}

	return pem.EncodeToMemory(&pem.Block{
		Type: blockType, Bytes: keyBytes,
	}), nil
}

// EncodeX509 will encode a single *x509.Certificate into PEM format.
func EncodeX509(cert *x509.Certificate) ([]byte, error) {
	caPem := bytes.NewBuffer([]byte{})
	err := pem.Encode(caPem, &pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	if err != nil {
		return nil, err
	}

	return caPem.Bytes(), nil
}

// EncodeX509Chain will encode a list of *x509.Certificates into a PEM format chain.
// Self-signed certificates are not included as per
// https://datatracker.ietf.org/doc/html/rfc5246#section-7.4.2
// Certificates are output in the order they're given; if the input is not ordered
// as specified in RFC5246 section 7.4.2, the resulting chain might not be valid
// for use in TLS.
func EncodeX509Chain(certs []*x509.Certificate) ([]byte, error) {
	if len(certs) == 0 {
		return nil, errors.New("no certificates in chain")
	}

	certPEM := bytes.NewBuffer([]byte{})
	for _, cert := range certs {
		if cert == nil {
			continue
		}

		if cert.CheckSignatureFrom(cert) == nil {
			// Don't include self-signed certificate
			continue
		}

		err := pem.Encode(certPEM, &pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
		if err != nil {
			return nil, err
		}
	}

	return certPEM.Bytes(), nil
}

// PublicKeysEqual compares two given public keys for equality.
// The definition of "equality" depends on the type of the public keys.
// Returns true if the keys are the same, false if they differ or an error if
// the key type of `a` cannot be determined.
func PublicKeysEqual(a, b crypto.PublicKey) (bool, error) {
	switch pub := a.(type) {
	case *rsa.PublicKey:
		return pub.Equal(b), nil
	case *ecdsa.PublicKey:
		return pub.Equal(b), nil
	case ed25519.PublicKey:
		return pub.Equal(b), nil
	default:
		return false, fmt.Errorf("unrecognised public key type: %T", a)
	}
}
