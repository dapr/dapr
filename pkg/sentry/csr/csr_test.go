package csr

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"testing"
	"time"

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
