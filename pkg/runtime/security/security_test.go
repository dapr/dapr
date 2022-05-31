package security

import (
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/sentry/certs"
)

var testRootCert = `-----BEGIN CERTIFICATE-----
MIIBjjCCATOgAwIBAgIQdZeGNuAHZhXSmb37Pnx2QzAKBggqhkjOPQQDAjAYMRYw
FAYDVQQDEw1jbHVzdGVyLmxvY2FsMB4XDTIwMDIwMTAwMzUzNFoXDTMwMDEyOTAw
MzUzNFowGDEWMBQGA1UEAxMNY2x1c3Rlci5sb2NhbDBZMBMGByqGSM49AgEGCCqG
SM49AwEHA0IABAeMFRst4JhcFpebfgEs1MvJdD7h5QkCbLwChRHVEUoaDqd1aYjm
bX5SuNBXz5TBEhHfTV3Objh6LQ2N+CBoCeOjXzBdMA4GA1UdDwEB/wQEAwIBBjAS
BgNVHRMBAf8ECDAGAQH/AgEBMB0GA1UdDgQWBBRBWthv5ZQ3vALl2zXWwAXSmZ+m
qTAYBgNVHREEETAPgg1jbHVzdGVyLmxvY2FsMAoGCCqGSM49BAMCA0kAMEYCIQDN
rQNOck4ENOhmLROE/wqH0MKGjE6P8yzesgnp9fQI3AIhAJaVPrZloxl1dWCgmNWo
Iklq0JnMgJU7nS+VpVvlgBN8
-----END CERTIFICATE-----`

func TestGetTrustAnchors(t *testing.T) {
	t.Run("invalid root cert", func(t *testing.T) {
		os.Setenv(certs.TrustAnchorsEnvVar, "111")
		os.Setenv(certs.CertChainEnvVar, "111")
		os.Setenv(certs.CertKeyEnvVar, "111")
		defer func() {
			os.Unsetenv(certs.TrustAnchorsEnvVar)
			os.Unsetenv(certs.CertChainEnvVar)
			os.Unsetenv(certs.CertKeyEnvVar)
		}()

		certChain, _ := GetCertChain()
		caPool, err := CertPool(certChain.Cert)
		assert.Error(t, err)
		assert.Nil(t, caPool)
	})

	t.Run("valid root cert", func(t *testing.T) {
		os.Setenv(certs.TrustAnchorsEnvVar, testRootCert)
		os.Setenv(certs.CertChainEnvVar, "111")
		os.Setenv(certs.CertKeyEnvVar, "111")
		defer func() {
			os.Unsetenv(certs.TrustAnchorsEnvVar)
			os.Unsetenv(certs.CertChainEnvVar)
			os.Unsetenv(certs.CertKeyEnvVar)
		}()

		certChain, err := GetCertChain()
		assert.Nil(t, err)
		caPool, err := CertPool(certChain.RootCA)
		assert.Nil(t, err)
		assert.NotNil(t, caPool)
	})
}

func TestGenerateSidecarCSR(t *testing.T) {
	// can't run this on Windows build agents, GH actions fails with "CryptAcquireContext: Provider DLL failed to initialize correctly."
	if runtime.GOOS == "windows" {
		return
	}

	t.Run("empty id", func(t *testing.T) {
		_, _, err := generateCSRAndPrivateKey("")
		assert.NotNil(t, err)
	})

	t.Run("with id", func(t *testing.T) {
		csr, pk, err := generateCSRAndPrivateKey("test")
		assert.Nil(t, err)
		assert.True(t, len(csr) > 0)
		assert.True(t, len(pk) > 0)
	})
}

func TestInitSidecarAuthenticator(t *testing.T) {
	os.Setenv(certs.TrustAnchorsEnvVar, testRootCert)
	os.Setenv(certs.CertChainEnvVar, "111")
	os.Setenv(certs.CertKeyEnvVar, "111")
	defer func() {
		os.Unsetenv(certs.TrustAnchorsEnvVar)
		os.Unsetenv(certs.CertChainEnvVar)
		os.Unsetenv(certs.CertKeyEnvVar)
	}()

	certChain, _ := GetCertChain()
	_, err := GetSidecarAuthenticator("localhost:5050", certChain)
	assert.NoError(t, err)
}
