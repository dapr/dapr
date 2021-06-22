package csr

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"

	"github.com/pkg/errors"

	"github.com/dapr/dapr/pkg/sentry/certs"
	"github.com/dapr/dapr/pkg/sentry/identity"
)

const (
	blockTypeECPrivateKey = "EC PRIVATE KEY" // EC private key
	blockTypePrivateKey   = "PRIVATE KEY"    // PKCS#8 plain private key
	encodeMsgCSR          = "CERTIFICATE REQUEST"
	encodeMsgCert         = "CERTIFICATE"
)

// The OID for the SAN extension (http://www.alvestrand.no/objectid/2.5.29.17.html)
var oidSubjectAlternativeName = asn1.ObjectIdentifier{2, 5, 29, 17}

// GenerateCSR creates a X.509 certificate sign request and private key.
func GenerateCSR(org string, pkcs8 bool) ([]byte, []byte, error) {
	key, err := certs.GenerateECPrivateKey()
	if err != nil {
		return nil, nil, errors.Wrap(err, "unable to generate private keys")
	}

	templ, err := genCSRTemplate(org)
	if err != nil {
		return nil, nil, errors.Wrap(err, "error generating csr template")
	}

	csrBytes, err := x509.CreateCertificateRequest(rand.Reader, templ, crypto.PrivateKey(key))
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to create CSR")
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
func generateBaseCert(ttl, skew time.Duration, publicKey interface{}) (*x509.Certificate, error) {
	serNum, err := newSerialNumber()
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	// Allow for clock skew with the NotBefore validity bound.
	notBefore := now.Add(-1 * skew)
	notAfter := now.Add(ttl)

	return &x509.Certificate{
		SerialNumber: serNum,
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		PublicKey:    publicKey,
	}, nil
}

func GenerateIssuerCertCSR(cn string, publicKey interface{}, ttl, skew time.Duration) (*x509.Certificate, error) {
	cert, err := generateBaseCert(ttl, skew, publicKey)
	if err != nil {
		return nil, err
	}

	cert.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	cert.Subject = pkix.Name{
		CommonName: cn,
	}
	cert.DNSNames = []string{cn}
	cert.IsCA = true
	cert.BasicConstraintsValid = true
	cert.SignatureAlgorithm = x509.ECDSAWithSHA256
	return cert, nil
}

// GenerateRootCertCSR returns a CA root cert x509 Certificate.
func GenerateRootCertCSR(org, cn string, publicKey interface{}, ttl, skew time.Duration) (*x509.Certificate, error) {
	cert, err := generateBaseCert(ttl, skew, publicKey)
	if err != nil {
		return nil, err
	}

	cert.KeyUsage = x509.KeyUsageCertSign
	cert.ExtKeyUsage = append(cert.ExtKeyUsage, x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth)
	cert.Subject = pkix.Name{
		CommonName:   cn,
		Organization: []string{org},
	}
	cert.DNSNames = []string{cn}
	cert.IsCA = true
	cert.BasicConstraintsValid = true
	cert.SignatureAlgorithm = x509.ECDSAWithSHA256
	return cert, nil
}

// GenerateCSRCertificate returns an x509 Certificate from a CSR, signing cert, public key, signing private key and duration.
func GenerateCSRCertificate(csr *x509.CertificateRequest, subject string, identityBundle *identity.Bundle, signingCert *x509.Certificate, publicKey interface{}, signingKey crypto.PrivateKey,
	ttl, skew time.Duration, isCA bool) ([]byte, error) {
	cert, err := generateBaseCert(ttl, skew, publicKey)
	if err != nil {
		return nil, errors.Wrap(err, "error generating csr certificate")
	}
	if isCA {
		cert.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	} else {
		cert.KeyUsage = x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment
		cert.ExtKeyUsage = append(cert.ExtKeyUsage, x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth)
	}

	if subject == "cluster.local" {
		cert.Subject = pkix.Name{
			CommonName: subject,
		}
		cert.DNSNames = []string{subject}
	}

	cert.Issuer = signingCert.Issuer
	cert.IsCA = isCA
	cert.IPAddresses = csr.IPAddresses
	cert.Extensions = csr.Extensions
	cert.BasicConstraintsValid = true
	cert.SignatureAlgorithm = csr.SignatureAlgorithm

	if identityBundle != nil {
		spiffeID, err := identity.CreateSPIFFEID(identityBundle.TrustDomain, identityBundle.Namespace, identityBundle.ID)
		if err != nil {
			return nil, errors.Wrap(err, "error generating spiffe id")
		}

		rv := []asn1.RawValue{
			{
				Bytes: []byte(spiffeID),
				Class: asn1.ClassContextSpecific,
				Tag:   asn1.TagOID,
			},
			{
				Bytes: []byte(fmt.Sprintf("%s.%s.svc.cluster.local", subject, identityBundle.Namespace)),
				Class: asn1.ClassContextSpecific,
				Tag:   2,
			},
		}

		b, err := asn1.Marshal(rv)
		if err != nil {
			return nil, errors.Wrap(err, "failed to marshal asn1 raw value for spiffe id")
		}

		cert.ExtraExtensions = append(cert.ExtraExtensions, pkix.Extension{
			Id:       oidSubjectAlternativeName,
			Value:    b,
			Critical: true, // According to x509 and SPIFFE specs, a SubjAltName extension must be critical if subject name and DNS are not present.
		})
	}

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
		if encodedKey, err = x509.MarshalPKCS8PrivateKey(privKey); err != nil {
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
		return nil, errors.Wrap(err, "error generating serial number")
	}
	return serialNum, nil
}
