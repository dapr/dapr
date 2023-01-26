package security

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"os"

	"github.com/dapr/dapr/pkg/credentials"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/sentry/certs"
	sentryConsts "github.com/dapr/dapr/pkg/sentry/consts"
	"github.com/dapr/kit/logger"
)

const (
	ecPKType = "EC PRIVATE KEY"
)

var log = logger.NewLogger("dapr.runtime.security")

func CertPool(certPem []byte) (*x509.CertPool, error) {
	cp := x509.NewCertPool()
	ok := cp.AppendCertsFromPEM(certPem)
	if !ok {
		return nil, errors.New("failed to append PEM root cert to x509 CertPool")
	}
	return cp, nil
}

func GetCertChain() (*credentials.CertChain, error) {
	trustAnchors := os.Getenv(sentryConsts.TrustAnchorsEnvVar)
	if trustAnchors == "" {
		return nil, fmt.Errorf("couldn't find trust anchors in environment variable %s", sentryConsts.TrustAnchorsEnvVar)
	}
	cert := os.Getenv(sentryConsts.CertChainEnvVar)
	if cert == "" {
		return nil, fmt.Errorf("couldn't find cert chain in environment variable %s", sentryConsts.CertChainEnvVar)
	}
	key := os.Getenv(sentryConsts.CertKeyEnvVar)
	if cert == "" {
		return nil, fmt.Errorf("couldn't find cert key in environment variable %s", sentryConsts.CertKeyEnvVar)
	}
	return &credentials.CertChain{
		RootCA: []byte(trustAnchors),
		Cert:   []byte(cert),
		Key:    []byte(key),
	}, nil
}

// GetSidecarAuthenticator returns a new authenticator with the extracted trust anchors.
func GetSidecarAuthenticator(sentryAddress string, certChain *credentials.CertChain) (Authenticator, error) {
	trustAnchors, err := CertPool(certChain.RootCA)
	if err != nil {
		return nil, err
	}
	log.Info("trust anchors and cert chain extracted successfully")

	return newAuthenticator(sentryAddress, trustAnchors, certChain.Cert, certChain.Key, generateCSRAndPrivateKey), nil
}

func generateCSRAndPrivateKey(ctx context.Context, id string) ([]byte, []byte, error) {
	if id == "" {
		return nil, nil, errors.New("id must not be empty")
	}

	key, err := certs.GenerateECPrivateKey()
	if err != nil {
		diag.DefaultMonitoring.MTLSInitFailed(ctx, "prikeygen")
		return nil, nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	encodedKey, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		diag.DefaultMonitoring.MTLSInitFailed(ctx, "prikeyenc")
		return nil, nil, err
	}
	keyPem := pem.EncodeToMemory(&pem.Block{Type: ecPKType, Bytes: encodedKey})

	csr := x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: id},
		DNSNames: []string{id},
	}
	csrb, err := x509.CreateCertificateRequest(rand.Reader, &csr, key)
	if err != nil {
		diag.DefaultMonitoring.MTLSInitFailed(ctx, "csr")
		return nil, nil, fmt.Errorf("failed to create sidecar csr: %w", err)
	}
	return csrb, keyPem, nil
}
