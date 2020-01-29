package csr

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"

	"github.com/dapr/dapr/pkg/sentry/certs"
)

const (
	blockTypeECPrivateKey = "EC PRIVATE KEY" // EC private key
	blockTypePrivateKey   = "PRIVATE KEY"    // PKCS#8 plain private key
	encodeMsgCSR          = "CERTIFICATE REQUEST"
	encodeMsgCert         = "CERTIFICATE"
)

// GenerateCSR creates a X.509 certificate sign request and private key.
func GenerateCSR(org string, pkcs8 bool) ([]byte, []byte, error) {
	key, err := certs.GenerateECPrivateKey()
	if err != nil {
		return nil, nil, fmt.Errorf("unable to generate private keys: %s", err)
	}

	templ, err := genCSRTemplate(org)
	if err != nil {
		return nil, nil, fmt.Errorf("error generating csr template: %s", err)
	}

	csrBytes, err := x509.CreateCertificateRequest(rand.Reader, templ, crypto.PrivateKey(key))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create CSR: %s", err)
	}

	crtPem, keyPem, err := encode(true, csrBytes, key, pkcs8)
	return crtPem, keyPem, err
}

func genCSRTemplate(org string) (*x509.CertificateRequest, error) {
	return &x509.CertificateRequest{
		Subject: pkix.Name{
			Organization: []string{org},
		},
	}, nil
}

// generateBaseCert returns a base non-CA cert that can be made a workload or CA cert
// By adding subjects, key usage and additional proerties.
func generateBaseCert(ttl time.Duration, publicKey interface{}) (*x509.Certificate, error) {
	serNum, err := newSerialNumber()
	if err != nil {
		return nil, err
	}

	now := time.Now()
	notAfter := now.Add(ttl)

	return &x509.Certificate{
		SerialNumber: serNum,
		NotBefore:    now,
		NotAfter:     notAfter,
		PublicKey:    publicKey,
	}, nil
}

func GenerateIssuerCertCSR(cn string, publicKey interface{}, ttl time.Duration) (*x509.Certificate, error) {
	cert, err := generateBaseCert(ttl, publicKey)
	if err != nil {
		return nil, err
	}

	cert.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	cert.Subject = pkix.Name{
		CommonName: cn,
	}
	cert.IsCA = true
	cert.BasicConstraintsValid = true
	cert.SignatureAlgorithm = x509.ECDSAWithSHA256
	return cert, nil
}

// GenerateRootCertCSR returns a CA root cert x509 Certificate
func GenerateRootCertCSR(org, cn string, publicKey interface{}, ttl time.Duration) (*x509.Certificate, error) {
	cert, err := generateBaseCert(ttl, publicKey)
	if err != nil {
		return nil, err
	}

	cert.KeyUsage = x509.KeyUsageCertSign
	cert.ExtKeyUsage = append(cert.ExtKeyUsage, x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth)
	cert.Subject = pkix.Name{
		CommonName:   cn,
		Organization: []string{org},
	}
	cert.IsCA = true
	cert.BasicConstraintsValid = true
	cert.SignatureAlgorithm = x509.ECDSAWithSHA256
	return cert, nil
}

// GenerateCSRCertificate returns an x509 Certificate from a CSR, signing cert, public key, signing private key and duration.
func GenerateCSRCertificate(csr *x509.CertificateRequest, subject string, signingCert *x509.Certificate, publicKey interface{}, signingKey crypto.PrivateKey,
	ttl time.Duration, isCA bool) ([]byte, error) {
	cert, err := generateBaseCert(ttl, publicKey)
	if err != nil {
		return nil, fmt.Errorf("error generating csr certificate: %s", err)
	}
	if isCA {
		cert.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	} else {
		cert.KeyUsage = x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment
		cert.ExtKeyUsage = append(cert.ExtKeyUsage, x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth)
	}

	cert.Subject = pkix.Name{
		CommonName: subject,
	}
	cert.Issuer = signingCert.Issuer
	cert.IsCA = isCA
	cert.DNSNames = csr.DNSNames
	cert.IPAddresses = csr.IPAddresses
	cert.Extensions = csr.Extensions
	cert.BasicConstraintsValid = true
	cert.SignatureAlgorithm = csr.SignatureAlgorithm
	return x509.CreateCertificate(rand.Reader, cert, signingCert, publicKey, signingKey)
}

func encode(csr bool, csrOrCert []byte, privKey *ecdsa.PrivateKey, pkcs8 bool) ([]byte, []byte, error) {
	encodeMsg := encodeMsgCert
	if csr {
		encodeMsg = encodeMsgCSR
	}
	csrOrCertPem := pem.EncodeToMemory(&pem.Block{Type: encodeMsg, Bytes: csrOrCert})

	var encodedKey, privPem []byte
	var err error

	if pkcs8 {
		if encodedKey, err = x509.MarshalECPrivateKey(privKey); err != nil {
			return nil, nil, err
		}
		privPem = pem.EncodeToMemory(&pem.Block{Type: blockTypePrivateKey, Bytes: encodedKey})
	} else {
		encodedKey, err = x509.MarshalECPrivateKey(privKey)
		if err != nil {
			return nil, nil, err
		}
		privPem = pem.EncodeToMemory(&pem.Block{Type: blockTypeECPrivateKey, Bytes: encodedKey})
	}
	return csrOrCertPem, privPem, nil
}

func newSerialNumber() (*big.Int, error) {
	serialNumLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNum, err := rand.Int(rand.Reader, serialNumLimit)
	if err != nil {
		return nil, fmt.Errorf("error generating serial number: %s", err)
	}
	return serialNum, nil
}
