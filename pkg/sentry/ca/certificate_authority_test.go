package ca

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/sentry/certs"
	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/dapr/dapr/pkg/sentry/identity"
)

const (
	rootCert = `-----BEGIN CERTIFICATE-----
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

	issuerCert = `-----BEGIN CERTIFICATE-----
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

	issuerKey = `-----BEGIN EC PRIVATE KEY-----
MHcCAQEEIPka7+VUgUXmJghUv2JAYn9Pow1o6T3r3dxrvamrdubboAoGCCqGSM49
AwEHoUQDQgAEkDB/emmKm1PwOpt50ZCEanV8VXToYsIBIYbSQ/+rmCyJObLAeUsg
zWtds/T7oYatEywym92pgjUlQ7Yz8HsB4w==
-----END EC PRIVATE KEY-----`

	allowedClockSkew = time.Minute * 10

	workloadCertTTL = time.Hour * 10
)

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
	conf.AllowedClockSkew = allowedClockSkew
	conf.WorkloadCertTTL = workloadCertTTL
	certAuth, _ := NewCertificateAuthority(conf)
	return certAuth
}

//nolint:gosec
func writeTestCredentialsToDisk() {
	os.WriteFile("ca.crt", []byte(rootCert), 0o644)
	os.WriteFile("issuer.crt", []byte(issuerCert), 0o644)
	os.WriteFile("issuer.key", []byte(issuerKey), 0o644)
}

func cleanupCredentials() {
	os.Remove("ca.crt")
	os.Remove("issuer.crt")
	os.Remove("issuer.key")
}

func TestCertValidity(t *testing.T) {
	t.Run("valid cert", func(t *testing.T) {
		cert := getTestCSR("test.a.com")
		certAuth := defaultCA{}

		err := certAuth.ValidateCSR(cert)
		assert.NoError(t, err)
	})

	t.Run("invalid cert", func(t *testing.T) {
		cert := getTestCSR("")
		certAuth := defaultCA{}

		err := certAuth.ValidateCSR(cert)
		assert.Error(t, err)
	})
}

func TestSignCSR(t *testing.T) {
	t.Run("valid csr positive ttl", func(t *testing.T) {
		writeTestCredentialsToDisk()
		defer cleanupCredentials()

		csr := getTestCSR("test.a.com")
		pk, _ := getECDSAPrivateKey()
		csrb, _ := x509.CreateCertificateRequest(rand.Reader, csr, pk)
		certPem := pem.EncodeToMemory(&pem.Block{Type: certs.BlockTypeCertificate, Bytes: csrb})

		certAuth := getTestCertAuth()
		err := certAuth.LoadOrStoreTrustBundle(context.Background())
		require.NoError(t, err)

		resp, err := certAuth.SignCSR(certPem, "test-subject", nil, time.Hour*24, false)
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, time.Now().UTC().Add(time.Hour*24+allowedClockSkew).Day(), resp.Certificate.NotAfter.UTC().Day())
	})

	t.Run("valid csr negative ttl", func(t *testing.T) {
		writeTestCredentialsToDisk()
		defer cleanupCredentials()

		csr := getTestCSR("test.a.com")
		pk, _ := getECDSAPrivateKey()
		csrb, _ := x509.CreateCertificateRequest(rand.Reader, csr, pk)
		certPem := pem.EncodeToMemory(&pem.Block{Type: certs.BlockTypeCertificate, Bytes: csrb})

		certAuth := getTestCertAuth()
		err := certAuth.LoadOrStoreTrustBundle(context.Background())
		require.NoError(t, err)

		resp, err := certAuth.SignCSR(certPem, "test-subject", nil, time.Hour*-1, false)
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, time.Now().UTC().Add(workloadCertTTL+allowedClockSkew).Day(), resp.Certificate.NotAfter.UTC().Day())
	})

	t.Run("invalid csr", func(t *testing.T) {
		writeTestCredentialsToDisk()
		defer cleanupCredentials()

		certPem := []byte("")

		certAuth := getTestCertAuth()
		err := certAuth.LoadOrStoreTrustBundle(context.Background())
		require.NoError(t, err)

		_, err = certAuth.SignCSR(certPem, "", nil, time.Hour*24, false)
		assert.Error(t, err)
	})

	t.Run("valid identity", func(t *testing.T) {
		writeTestCredentialsToDisk()
		defer cleanupCredentials()

		csr := getTestCSR("test.a.com")
		pk, _ := getECDSAPrivateKey()
		csrb, _ := x509.CreateCertificateRequest(rand.Reader, csr, pk)
		certPem := pem.EncodeToMemory(&pem.Block{Type: certs.BlockTypeCertificate, Bytes: csrb})

		certAuth := getTestCertAuth()
		err := certAuth.LoadOrStoreTrustBundle(context.Background())
		require.NoError(t, err)

		bundle := identity.NewBundle("app", "default", "public")
		resp, err := certAuth.SignCSR(certPem, "test-subject", bundle, time.Hour*24, false)
		assert.NoError(t, err)
		assert.NotNil(t, resp)

		oidSubjectAlternativeName := asn1.ObjectIdentifier{2, 5, 29, 17}

		extFound := false
		for _, ext := range resp.Certificate.Extensions {
			if ext.Id.Equal(oidSubjectAlternativeName) {
				var sequence asn1.RawValue
				val, err := asn1.Unmarshal(ext.Value, &sequence)
				assert.NoError(t, err)
				assert.True(t, true, len(val) != 0)

				for bytes := sequence.Bytes; len(bytes) > 0; {
					var rawValue asn1.RawValue
					var err error

					bytes, err = asn1.Unmarshal(bytes, &rawValue)
					assert.NoError(t, err)

					id := string(rawValue.Bytes)
					if strings.HasPrefix(id, "spiffe://") {
						assert.Equal(t, "spiffe://public/ns/default/app", id)
						extFound = true
					}
				}
			}
		}

		if !extFound {
			t.Error("SAN extension not found in certificate")
		}
	})
}

func TestCACertsGeneration(t *testing.T) {
	defer cleanupCredentials()

	ca := getTestCertAuth()
	err := ca.LoadOrStoreTrustBundle(context.Background())

	assert.NoError(t, err)
	assert.True(t, len(ca.GetCACertBundle().GetRootCertPem()) > 0)
	assert.True(t, len(ca.GetCACertBundle().GetIssuerCertPem()) > 0)
}

func TestShouldCreateCerts(t *testing.T) {
	t.Run("certs exist, should not create", func(t *testing.T) {
		writeTestCredentialsToDisk()
		defer cleanupCredentials()

		a := getTestCertAuth()
		r := shouldCreateCerts(context.Background(), a.(*defaultCA).config)
		assert.False(t, r)
	})

	t.Run("certs do not exist, should create", func(t *testing.T) {
		a := getTestCertAuth()
		r := shouldCreateCerts(context.Background(), a.(*defaultCA).config)
		assert.True(t, r)
	})
}

func TestDetectCertificates(t *testing.T) {
	t.Run("detected before timeout", func(t *testing.T) {
		writeTestCredentialsToDisk()
		defer cleanupCredentials()

		a := getTestCertAuth()
		rootCertPath := a.(*defaultCA).config.RootCertPath
		err := detectCertificates(rootCertPath)
		assert.NoError(t, err)
	})

	// this is a negative test scenario for the one above that doesn't require waiting the full 30s timeout.
	// it's meant to check that detectCertificates doesn't detect the certs before they are loaded.
	t.Run("cert arrives on disk after 2s", func(t *testing.T) {
		defer cleanupCredentials()

		a := getTestCertAuth()
		rootCertPath := a.(*defaultCA).config.RootCertPath

		go func() {
			time.Sleep(time.Second * 2)
			writeTestCredentialsToDisk()
		}()

		var start time.Time
		var err error
		done := make(chan bool, 1)

		go func() {
			start = time.Now()
			err = detectCertificates(rootCertPath)
			done <- true
		}()

		<-done
		assert.NoError(t, err)
		assert.True(t, time.Since(start).Seconds() >= 2)
	})
}
