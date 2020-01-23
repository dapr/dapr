package ca

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func getTestCSR(name string) *x509.CertificateRequest {
	return &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: name},
		DNSNames: []string{name},
	}
}

func TestCertValidity(t *testing.T) {
	t.Run("valid cert", func(t *testing.T) {
		cert := getTestCSR("test.a.com")
		certAuth := defaultCA{
			issuerLock: &sync.RWMutex{},
		}

		err := certAuth.ValidateCSR(cert)
		assert.Nil(t, err)
	})

	t.Run("invalid cert", func(t *testing.T) {
		cert := getTestCSR("")
		certAuth := defaultCA{
			issuerLock: &sync.RWMutex{},
		}

		err := certAuth.ValidateCSR(cert)
		assert.NotNil(t, err)
	})
}
