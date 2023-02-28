package certs

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
)

const (
	BlockTypeCertificate     = "CERTIFICATE"
	BlockTypeECPrivateKey    = "EC PRIVATE KEY"
	BlockTypePKCS1PrivateKey = "RSA PRIVATE KEY"
	BlockTypePKCS8PrivateKey = "PRIVATE KEY"
)

// Credentials holds a certificate and private key.
type Credentials struct {
	PrivateKey  crypto.PrivateKey
	Certificate *x509.Certificate
}

// DecodePEMKey takes a key PEM byte array and returns an object that represents
// either an RSA or EC private key.
func DecodePEMKey(key []byte) (crypto.PrivateKey, error) {
	block, _ := pem.Decode(key)
	if block == nil {
		return nil, errors.New("key is not PEM encoded")
	}
	switch block.Type {
	case BlockTypeECPrivateKey:
		return x509.ParseECPrivateKey(block.Bytes)
	case BlockTypePKCS1PrivateKey:
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	case BlockTypePKCS8PrivateKey:
		return x509.ParsePKCS8PrivateKey(block.Bytes)
	default:
		return nil, fmt.Errorf("unsupported block type %s", block.Type)
	}
}

// DecodePEMCertificates takes a PEM-encoded x509 certificates byte array and returns
// all certificates in a slice of x509.Certificate objects.
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
	return certs, nil
}

func decodeCertificatePEM(crtb []byte) (*x509.Certificate, []byte, error) {
	block, crtb := pem.Decode(crtb)
	if block == nil {
		return nil, crtb, errors.New("invalid PEM certificate")
	}
	if block.Type != BlockTypeCertificate {
		return nil, nil, nil
	}
	c, err := x509.ParseCertificate(block.Bytes)
	return c, crtb, err
}

// PEMCredentialsFromFiles takes a path for a key/cert pair and returns a validated Credentials wrapper.
func PEMCredentialsFromFiles(certPem, keyPem []byte) (*Credentials, error) {
	pk, err := DecodePEMKey(keyPem)
	if err != nil {
		return nil, err
	}

	crts, err := DecodePEMCertificates(certPem)
	if err != nil {
		return nil, err
	}

	if len(crts) == 0 {
		return nil, errors.New("no certificates found")
	}

	match := matchCertificateAndKey(pk, crts[0])
	if !match {
		return nil, errors.New("error validating credentials: public and private key pair do not match")
	}

	creds := &Credentials{
		PrivateKey:  pk,
		Certificate: crts[0],
	}

	return creds, nil
}

func matchCertificateAndKey(pk any, cert *x509.Certificate) bool {
	switch key := pk.(type) {
	case *ecdsa.PrivateKey:
		if cert.PublicKeyAlgorithm != x509.ECDSA {
			return false
		}
		pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
		return ok && pub.Equal(key.Public())
	case *rsa.PrivateKey:
		if cert.PublicKeyAlgorithm != x509.RSA {
			return false
		}
		pub, ok := cert.PublicKey.(*rsa.PublicKey)
		return ok && pub.Equal(key.Public())
	case ed25519.PrivateKey:
		if cert.PublicKeyAlgorithm != x509.Ed25519 {
			return false
		}
		pub, ok := cert.PublicKey.(ed25519.PublicKey)
		return ok && pub.Equal(key.Public())
	default:
		return false
	}
}

func certPoolFromCertificates(certs []*x509.Certificate) *x509.CertPool {
	pool := x509.NewCertPool()
	for _, c := range certs {
		pool.AddCert(c)
	}
	return pool
}

// CertPoolFromPEMString returns a CertPool from a PEM encoded certificates string.
func CertPoolFromPEM(certPem []byte) (*x509.CertPool, error) {
	certs, err := DecodePEMCertificates(certPem)
	if err != nil {
		return nil, err
	}
	if len(certs) == 0 {
		return nil, errors.New("no certificates found")
	}

	return certPoolFromCertificates(certs), nil
}

// ParsePemCSR constructs a x509 Certificate Request using the
// given PEM-encoded certificate signing request.
func ParsePemCSR(csrPem []byte) (*x509.CertificateRequest, error) {
	block, _ := pem.Decode(csrPem)
	if block == nil {
		return nil, errors.New("certificate signing request is not properly encoded")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse X.509 certificate signing request: %w", err)
	}
	return csr, nil
}

// GenerateECPrivateKey returns a new EC Private Key.
func GenerateECPrivateKey() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}
