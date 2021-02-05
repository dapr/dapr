package csr

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"testing"
	"time"

	"github.com/dapr/dapr/pkg/sentry/certs"
	"github.com/stretchr/testify/assert"
)

func TestGenerateCSRTemplate(t *testing.T) {
	t.Run("valid csr template", func(t *testing.T) {
		tmpl, err := genCSRTemplate("test-org")
		assert.Nil(t, err)
		assert.Equal(t, "test-org", tmpl.Subject.Organization[0])
	})
}

func TestGenerateBaseCertificate(t *testing.T) {
	pk, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	cert, err := generateBaseCert(time.Second*5, pk)

	assert.NoError(t, err)
	assert.Equal(t, cert.PublicKey, pk)
}

func TestGenerateIssuerCertCSR(t *testing.T) {
	pk, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	cert, err := GenerateIssuerCertCSR("name", pk, time.Second*5)

	assert.NoError(t, err)
	assert.Equal(t, "name", cert.DNSNames[0])
	assert.Equal(t, "name", cert.Subject.CommonName)
}

func TestGenerateRootCertCSR(t *testing.T) {
	pk, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	cert, err := GenerateRootCertCSR("org", "name", pk, time.Second*5)

	assert.NoError(t, err)
	assert.Equal(t, "name", cert.Subject.CommonName)
	assert.Equal(t, "org", cert.Subject.Organization[0])
}

func TestCertSerialNumber(t *testing.T) {
	n, err := newSerialNumber()
	assert.NoError(t, err)
	assert.NotNil(t, n)
}

func TestGenerateCSR(t *testing.T) {
	c, b, err := GenerateCSR("org", false)
	assert.NoError(t, err)
	assert.True(t, len(b) > 0)
	assert.True(t, len(c) > 0)
}

func TestEncode(t *testing.T) {
	var CertTests = []struct {
		org     string `json:"org"`
		isPkcs8 bool   `json:"is_pkcs8"`
	}{
		{
			org:     "dapr.io",
			isPkcs8: false,
		},
		{
			org:     "dapr.io",
			isPkcs8: true,
		},
	}
	var errs = make([]error, len(CertTests))
	for index, test := range CertTests {
		key, _ := certs.GenerateECPrivateKey()
		templ, _ := genCSRTemplate(test.org)
		csrBytes, err := x509.CreateCertificateRequest(rand.Reader, templ, crypto.PrivateKey(key))
		if err != nil {
			errs[index] = err
			return
		}
		crtPem, keyPem, err := encode(true, csrBytes, key, test.isPkcs8)
		if err != nil {
			errs[index] = err
			return
		}
		_, err = PEMCredentialRequestsFromFiles(crtPem, keyPem)
		errs[index] = err
	}
	assert.Nil(t, errs[0])
	assert.Nil(t, errs[1])
}
