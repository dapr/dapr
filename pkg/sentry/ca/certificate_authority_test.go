package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io/ioutil"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/dapr/dapr/pkg/sentry/certs"
	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/stretchr/testify/assert"
)

const rootCert = `-----BEGIN CERTIFICATE-----
MIIBgjCCASigAwIBAgIRAIrX/pDJ+p4f6dujmOifvbkwCgYIKoZIzj0EAwIwFDES
MBAGA1UEAxMJbG9jYWxob3N0MB4XDTIwMDEyMjE4MTQwMloXDTMwMDExOTE4MTQw
MlowFDESMBAGA1UEAxMJbG9jYWxob3N0MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcD
QgAEm/SwnezTXGe2owu3n0Ov200siD6tCFHnMSW203q5G3uFXzrKEYTocTFBvxeb
xkwS9NqC+RgHxHC65NFKkkakg6NbMFkwDgYDVR0PAQH/BAQDAgEGMBIGA1UdEwEB
/wQIMAYBAf8CAQEwHQYDVR0OBBYEFIdC7dnsSlYk+fCpA2xT3GJOwJIGMBQGA1Ud
EQQNMAuCCWxvY2FsaG9zdDAKBggqhkjOPQQDAgNIADBFAiBdYhY/fv+W9sgf2GeO
xlY/D2Y8sYvui1VXj0q0HHElgAIhAK2jBa2onvy/K1Epk9q7d3pZi5Kqz0NsOzFV
0qL4GK10
-----END CERTIFICATE-----`

const issuerCert = `-----BEGIN CERTIFICATE-----
MIIBgjCCASigAwIBAgIRANfF9X4LJ314GZjUe17qsQ0wCgYIKoZIzj0EAwIwFDES
MBAGA1UEAxMJbG9jYWxob3N0MB4XDTIwMDEyMjE4MTQyMVoXDTIxMDEyMTE4MTQy
MVowFDESMBAGA1UEAxMJbG9jYWxob3N0MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcD
QgAEkDB/emmKm1PwOpt50ZCEanV8VXToYsIBIYbSQ/+rmCyJObLAeUsgzWtds/T7
oYatEywym92pgjUlQ7Yz8HsB46NbMFkwDgYDVR0PAQH/BAQDAgEGMBIGA1UdEwEB
/wQIMAYBAf8CAQAwHQYDVR0OBBYEFG3ToPqtvBaQAWKS80CB0emXwGsOMBQGA1Ud
EQQNMAuCCWxvY2FsaG9zdDAKBggqhkjOPQQDAgNIADBFAiEAuyt3Qcx1uMImrHdx
flWXeGl/HN2HQQTcszk2fRvb0c4CICs60ZlkjXoRA8rLLi1wnfkUS8rQ1PT3R/Mp
2ZK4OZle
-----END CERTIFICATE-----`

const issuerKey = `-----BEGIN EC PRIVATE KEY-----
MHcCAQEEIPka7+VUgUXmJghUv2JAYn9Pow1o6T3r3dxrvamrdubboAoGCCqGSM49
AwEHoUQDQgAEkDB/emmKm1PwOpt50ZCEanV8VXToYsIBIYbSQ/+rmCyJObLAeUsg
zWtds/T7oYatEywym92pgjUlQ7Yz8HsB4w==
-----END EC PRIVATE KEY-----`

func getTestCSR(name string) *x509.CertificateRequest {
	return &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: name},
		DNSNames: []string{name},
	}
}

func getECDSAPrivateKey() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}

func getTestCertAuth() CertificateAuthority {
	conf, _ := config.FromConfigName("")
	conf.RootCertPath = "./ca.crt"
	conf.IssuerCertPath = "./issuer.crt"
	conf.IssuerKeyPath = "./issuer.key"
	certAuth, _ := NewCertificateAuthority(conf)
	return certAuth
}

// nolint:gosec
func writeTestCredentialsToDisk() {
	ioutil.WriteFile("ca.crt", []byte(rootCert), 0644)
	ioutil.WriteFile("issuer.crt", []byte(issuerCert), 0644)
	ioutil.WriteFile("issuer.key", []byte(issuerKey), 0644)
}

func cleanupCredentials() {
	os.Remove("ca.crt")
	os.Remove("issuer.crt")
	os.Remove("issuer.key")
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

func TestSignCSR(t *testing.T) {
	t.Run("valid csr", func(t *testing.T) {
		writeTestCredentialsToDisk()
		defer cleanupCredentials()

		csr := getTestCSR("test.a.com")
		pk, _ := getECDSAPrivateKey()
		csrb, _ := x509.CreateCertificateRequest(rand.Reader, csr, pk)
		certPem := pem.EncodeToMemory(&pem.Block{Type: certs.Certificate, Bytes: csrb})

		certAuth := getTestCertAuth()
		certAuth.LoadOrStoreTrustBundle()

		resp, err := certAuth.SignCSR(certPem, "test-subject", time.Hour*24, false)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, time.Now().UTC().AddDate(0, 0, 1).Day(), resp.Certificate.NotAfter.UTC().Day())
	})

	t.Run("invalid csr", func(t *testing.T) {
		writeTestCredentialsToDisk()
		defer cleanupCredentials()

		certPem := []byte("")

		certAuth := getTestCertAuth()
		certAuth.LoadOrStoreTrustBundle()

		_, err := certAuth.SignCSR(certPem, "", time.Hour*24, false)
		assert.NotNil(t, err)
	})
}
