package security

import (
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"os"

	"github.com/dapr/dapr/pkg/sentry/certs"
	log "github.com/sirupsen/logrus"
)

const (
	ecPKType = "EC PRIVATE KEY"
)

func getTrustAnchors() (*x509.CertPool, error) {
	trustAnchors := os.Getenv(certs.TrustAnchorsEnvVar)
	if trustAnchors == "" {
		return nil, fmt.Errorf("couldn't find trust anchors in environment variable %s", certs.TrustAnchorsEnvVar)
	}

	cp := x509.NewCertPool()
	ok := cp.AppendCertsFromPEM([]byte(trustAnchors))
	if !ok {
		return nil, errors.New("failed to append PEM root cert to x509 CertPool")
	}
	return cp, nil
}

// GetSidecarAuthenticator returns a new authenticator with the extracted trust anchors
func GetSidecarAuthenticator(id, sentryAddress string) (Authenticator, error) {
	trustAnchors, err := getTrustAnchors()
	if err != nil {
		return nil, err
	}
	log.Info("trust anchors extracted successfully")

	return newAuthenticator(sentryAddress, trustAnchors, generateCSRAndPrivateKey), nil
}

func generateCSRAndPrivateKey(id string) ([]byte, []byte, error) {
	if id == "" {
		return nil, nil, errors.New("id must not be empty")
	}

	key, err := certs.GenerateECPrivateKey()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate private key: %s", err)
	}

	encodedKey, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	keyPem := pem.EncodeToMemory(&pem.Block{Type: ecPKType, Bytes: encodedKey})

	csr := x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: id},
		DNSNames: []string{id},
	}
	csrb, err := x509.CreateCertificateRequest(rand.Reader, &csr, key)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create sidecar csr: %s", err)
	}
	return csrb, keyPem, nil
}
