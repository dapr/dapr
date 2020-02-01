package security

import (
	"crypto/x509"
	"testing"

	"github.com/stretchr/testify/assert"
)

func mockGenCSR(id string) ([]byte, []byte, error) {
	return []byte{1}, []byte{2}, nil
}

func getTestAuthenticator() Authenticator {
	return newAuthenticator("test", x509.NewCertPool(), mockGenCSR)
}

func TestGetTrustAuthAnchors(t *testing.T) {
	a := getTestAuthenticator()
	ta := a.GetTrustAnchors()
	assert.NotNil(t, ta)
}

func TestGetCurrentSignedCert(t *testing.T) {
	a := getTestAuthenticator()
	a.(*authenticator).currentSignedCert = &SignedCertificate{}
	c := a.GetCurrentSignedCert()
	assert.NotNil(t, c)
}
