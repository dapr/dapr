package ca

import (
	"crypto/x509"
	"github.com/dapr/dapr/pkg/sentry/certs"
	"time"
)

// TrustRootBundle represents the root certificate, issuer certificate and their
// Respective expiry dates.
type TrustRootBundler interface {
	GetIssuerCertPem() []byte
	GetIssuerCertExpiry() time.Time
	GetTrustAnchors() *x509.CertPool
	GetTrustDomain() string
}

type trustRootBundle struct {
	issuerCreds  *certs.Credentials
	trustAnchors *x509.CertPool
	trustDomain  string
}

func (t *trustRootBundle) GetIssuerCertPem() []byte {
	return t.issuerCreds.Certificate.Raw
}

func (t *trustRootBundle) GetIssuerCertExpiry() time.Time {
	return t.issuerCreds.Certificate.NotAfter
}

func (t *trustRootBundle) GetTrustAnchors() *x509.CertPool {
	return t.trustAnchors
}

func (t *trustRootBundle) GetTrustDomain() string {
	return t.trustDomain
}
