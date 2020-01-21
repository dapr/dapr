package ca

import (
	"crypto"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io/ioutil"
	"sync"
	"time"

	"github.com/dapr/dapr/pkg/sentry/certs"
	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/dapr/dapr/pkg/sentry/csr"
	log "github.com/sirupsen/logrus"
)

const (
	rootCertSecretName   = "daprRootCert"
	issuerCertSecretName = "daprIssuerCert"
	certSecretKey        = "cert"
	pkSecretKey          = "privateKey"
)

// CertificateAuthority represents an interface for a compliant Certificate Authority.
// Responsibilities include loading trust anchors and issuer certs, providing safe access to the trust bundle,
// Validating and signing CSRs
type CertificateAuthority interface {
	LoadOrStoreTrustBundle() error
	GetCACertBundle() TrustRootBundle
	SignCSR(csr *x509.CertificateRequest, ttl time.Duration, isCA bool) (*SignedCertificate, error)
	ValidateCSR(csr *x509.CertificateRequest) error
}

func NewCertificateAuthority(config config.SentryConfig) (CertificateAuthority, error) {
	// Load future external CAs from components-contrib.
	switch config.CAStore {
	default:
		return &defaultCA{
			config:     config,
			issuerLock: &sync.RWMutex{},
		}, nil
	}
}

type defaultCA struct {
	bundle     *trustRootBundle
	config     config.SentryConfig
	issuerLock *sync.RWMutex
}

type SignedCertificate struct {
	Certificate *x509.Certificate
	CertPEM     []byte
	TrustChain  []*x509.Certificate
}

func (s *SignedCertificate) GetChain() [][]byte {
	chain := make([][]byte, len(s.TrustChain)+1)
	chain[0] = s.Certificate.Raw
	for i, c := range s.TrustChain {
		chain[len(s.TrustChain)-i] = c.Raw
	}
	return chain
}

// LoadOrStoreTrustBundle loads the root cert and issuer cert from the configured secret store.
// Validation is perfromed and a protected trust bundle is created holding the trust anchors
// and issuer credentials. If successful, a watcher is launched to keep track of the issuer expiration.
func (c *defaultCA) LoadOrStoreTrustBundle() error {
	bundle, err := c.validateAndBuildTrustBundle()
	if err != nil {
		return err
	}

	c.bundle = bundle
	go c.startIssuerWatcher()
	return nil
}

// GetCACertBundle returns the Trust Root Bundle
func (c *defaultCA) GetCACertBundle() TrustRootBundle {
	return c.bundle
}

// SignCSR signs a request with a PEM encoded CSR cert and duration.
// If isCA is set to true, a CA cert will be issued. If isCA is set to false, a workload
// Certificate will be issued instead.
func (c *defaultCA) SignCSR(req *x509.CertificateRequest, ttl time.Duration, isCA bool) (*SignedCertificate, error) {
	c.issuerLock.RLock()
	defer c.issuerLock.RUnlock()

	certLifetime := ttl
	if certLifetime.Seconds() < 0 {
		certLifetime = c.config.WorkloadCertTTL
	}

	signingCert := c.bundle.issuerCreds.Certificate
	signingKey := c.bundle.issuerCreds.PrivateKey

	crtb, err := csr.GenerateCSRCertificate(req, signingCert, req.PublicKey, signingKey.Key, ttl, isCA)
	if err != nil {
		return nil, fmt.Errorf("error signing csr: %s", err)
	}

	cert, err := x509.ParseCertificate(crtb)
	if err != nil {
		return nil, fmt.Errorf("error parsing cert: %s", err)
	}

	certPem := pem.EncodeToMemory(&pem.Block{
		Type:  certs.Certificate,
		Bytes: crtb,
	})

	return &SignedCertificate{
		Certificate: cert,
		TrustChain:  append(c.bundle.issuerCreds.TrustChain, c.bundle.issuerCreds.Certificate),
		CertPEM:     certPem,
	}, nil
}

func (c *defaultCA) ValidateCSR(csr *x509.CertificateRequest) error {
	if csr.Subject.CommonName == "" {
		return errors.New("cannot validate request: missing common name")
	}
	return nil
}

func (c *defaultCA) validateAndBuildTrustBundle() (*trustRootBundle, error) {
	issuerCreds, err := certs.PEMCredentialsFromFiles(c.config.IssuerKeyPath, c.config.IssuerCertPath)
	if err != nil {
		return nil, fmt.Errorf("error reading PEM credentials: %s", err)
	}

	rootCertBytes, err := ioutil.ReadFile(c.config.RootCertPath)
	if err != nil {
		return nil, fmt.Errorf("error reading root cert from disk: %s", err)
	}

	trustAnchors, err := certs.CertPoolFromPEM(rootCertBytes)
	if err != nil {
		return nil, fmt.Errorf("error parsing cert pool for trust anchors: %s", err)
	}

	return &trustRootBundle{
		issuerCreds:  issuerCreds,
		trustAnchors: trustAnchors,
	}, nil
}

func (c *defaultCA) startIssuerWatcher() {
	log.Infof("starting watch on FS: issuer cert %s, issuer key: %s", c.config.IssuerCertPath, c.config.IssuerKeyPath)
	//TODO: implement issuer credentials watcher
}

// GenerateSidecarCertificate generates a keypair and returns a new certificate
func (c *defaultCA) GenerateSidecarCertificate(dnsName string) (*certs.Credentials, error) {
	pk, err := certs.GeneratePrivateKey()
	if err != nil {
		return nil, err
	}

	csr := &x509.CertificateRequest{
		Subject:   pkix.Name{CommonName: dnsName},
		DNSNames:  []string{dnsName},
		PublicKey: &pk.PublicKey,
	}

	signed, err := c.SignCSR(csr, -1, false)
	if err != nil {
		err = fmt.Errorf("error signing csr: %s", err)
		log.Error(err)
		return nil, err
	}

	return &certs.Credentials{
		PrivateKey: &certs.PrivateKey{
			Key:  crypto.PrivateKey(pk),
			Type: certs.ECPrivateKey,
		},
		Certificate: signed.Certificate,
	}, nil
}
