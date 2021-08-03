package ca

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/sentry/certs"
)

func getTestCert() *x509.Certificate {
	return &x509.Certificate{
		SerialNumber: big.NewInt(1653),
		Subject: pkix.Name{
			Organization:  []string{"ORGANIZATION_NAME"},
			Country:       []string{"COUNTRY_CODE"},
			Province:      []string{"PROVINCE"},
			Locality:      []string{"CITY"},
			StreetAddress: []string{"ADDRESS"},
			PostalCode:    []string{"POSTAL_CODE"},
		},
		NotBefore:             time.Now().UTC(),
		NotAfter:              time.Now().UTC().AddDate(10, 0, 0),
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
}

func TestBundleIssuerExpiry(t *testing.T) {
	bundle := trustRootBundle{}
	issuerCert := getTestCert()
	bundle.issuerCreds = &certs.Credentials{
		Certificate: issuerCert,
	}

	assert.Equal(t, issuerCert.NotAfter.String(), bundle.GetIssuerCertExpiry().String())
}

func TestBundleIssuerCertMatch(t *testing.T) {
	bundle := trustRootBundle{}
	issuerCert := getTestCert()
	bundle.issuerCreds = &certs.Credentials{
		Certificate: issuerCert,
	}

	assert.Equal(t, issuerCert.Raw, bundle.GetIssuerCertPem())
}

func TestRootCertPEM(t *testing.T) {
	bundle := trustRootBundle{}
	cert := getTestCert()
	bundle.rootCertPem = cert.Raw

	assert.Equal(t, cert.Raw, bundle.GetRootCertPem())
}

func TestIssuerCertPEM(t *testing.T) {
	bundle := trustRootBundle{}
	cert := getTestCert()
	bundle.issuerCertPem = cert.Raw

	assert.Equal(t, cert.Raw, bundle.GetIssuerCertPem())
}

func TestTrustDomain(t *testing.T) {
	td := "td1"
	bundle := trustRootBundle{}
	bundle.trustDomain = td

	assert.Equal(t, td, bundle.GetTrustDomain())
}

func TestTrustAnchors(t *testing.T) {
	bundle := trustRootBundle{}
	pool := &x509.CertPool{}
	bundle.trustAnchors = pool

	assert.Equal(t, pool, bundle.GetTrustAnchors())
}
