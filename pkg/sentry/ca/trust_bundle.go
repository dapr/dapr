package ca

import (
	"crypto/x509"
	"github.com/dapr/dapr/pkg/sentry/certs"
	"time"
)

// TrustRootBundle represents the root certificate, issuer certificate and their
// Respective expiry dates.
type TrustRootBundle interface {
	GetIssuerCertPem() []byte
	GetIssuerCertExpiry() time.Time
	GetTrustAnchors() *x509.CertPool
}

type trustRootBundle struct {
	rootCertExpiry   time.Time
	issuerCertExpiry time.Time
	issuerCreds      *certs.Credentials
	trustAnchors     *x509.CertPool
}

func (t *trustRootBundle) GetIssuerCertPem() []byte {
	return t.issuerCreds.Certificate.Raw
}

func (t *trustRootBundle) GetIssuerCertExpiry() time.Time {
	return t.issuerCertExpiry
}

func (t *trustRootBundle) GetTrustAnchors() *x509.CertPool {
	return t.trustAnchors
}
