package ca

import (
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"sync"
	"time"

	"github.com/dapr/dapr/pkg/sentry/certs"
	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/dapr/dapr/pkg/sentry/csr"
	log "github.com/sirupsen/logrus"
)

const (
	caOrg                      = "dapr.io/sentry"
	caCommonName               = "sentry"
	selfSignedRootCertLifetime = time.Hour * 8760
)

// CertificateAuthority represents an interface for a compliant Certificate Authority.
// Responsibilities include loading trust anchors and issuer certs, providing safe access to the trust bundle,
// Validating and signing CSRs
type CertificateAuthority interface {
	LoadOrStoreTrustBundle() error
	GetCACertBundle() TrustRootBundler
	SignCSR(csrPem []byte, subject string, ttl time.Duration, isCA bool) (*SignedCertificate, error)
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
}

// LoadOrStoreTrustBundle loads the root cert and issuer cert from the configured secret store.
// Validation is performed and a protected trust bundle is created holding the trust anchors
// and issuer credentials. If successful, a watcher is launched to keep track of the issuer expiration.
func (c *defaultCA) LoadOrStoreTrustBundle() error {
	bundle, err := c.validateAndBuildTrustBundle()
	if err != nil {
		return err
	}

	c.bundle = bundle
	return nil
}

// GetCACertBundle returns the Trust Root Bundle
func (c *defaultCA) GetCACertBundle() TrustRootBundler {
	return c.bundle
}

// SignCSR signs a request with a PEM encoded CSR cert and duration.
// If isCA is set to true, a CA cert will be issued. If isCA is set to false, a workload
// Certificate will be issued instead.
func (c *defaultCA) SignCSR(csrPem []byte, subject string, ttl time.Duration, isCA bool) (*SignedCertificate, error) {
	c.issuerLock.RLock()
	defer c.issuerLock.RUnlock()

	certLifetime := ttl
	if certLifetime.Seconds() < 0 {
		certLifetime = c.config.WorkloadCertTTL
	}

	certLifetime += c.config.AllowedClockSkew

	signingCert := c.bundle.issuerCreds.Certificate
	signingKey := c.bundle.issuerCreds.PrivateKey

	cert, err := certs.ParsePemCSR(csrPem)
	if err != nil {
		return nil, fmt.Errorf("error parsing csr pem: %s", err)
	}

	crtb, err := csr.GenerateCSRCertificate(cert, subject, signingCert, cert.PublicKey, signingKey.Key, certLifetime, isCA)
	if err != nil {
		return nil, fmt.Errorf("error signing csr: %s", err)
	}

	csrCert, err := x509.ParseCertificate(crtb)
	if err != nil {
		return nil, fmt.Errorf("error parsing cert: %s", err)
	}

	certPem := pem.EncodeToMemory(&pem.Block{
		Type:  certs.Certificate,
		Bytes: crtb,
	})

	return &SignedCertificate{
		Certificate: csrCert,
		CertPEM:     certPem,
	}, nil
}

func (c *defaultCA) ValidateCSR(csr *x509.CertificateRequest) error {
	if csr.Subject.CommonName == "" {
		return errors.New("cannot validate request: missing common name")
	}
	return nil
}

func shouldCreateCerts(conf config.SentryConfig) bool {
	if _, err := os.Stat(conf.RootCertPath); os.IsNotExist(err) {
		return true
	}
	b, err := ioutil.ReadFile(conf.IssuerCertPath)
	if err != nil {
		return true
	}
	return len(b) == 0
}

func (c *defaultCA) validateAndBuildTrustBundle() (*trustRootBundle, error) {
	var issuerCreds *certs.Credentials
	var err error
	var rootCertBytes []byte
	var issuerCertBytes []byte

	// certs exist on disk, load them
	if !shouldCreateCerts(c.config) {
		issuerCreds, err = certs.PEMCredentialsFromFiles(c.config.IssuerKeyPath, c.config.IssuerCertPath)
		if err != nil {
			return nil, fmt.Errorf("error reading PEM credentials: %s", err)
		}

		rootCertBytes, err = ioutil.ReadFile(c.config.RootCertPath)
		if err != nil {
			return nil, fmt.Errorf("error reading root cert from disk: %s", err)
		}

		issuerCertBytes, err = ioutil.ReadFile(c.config.IssuerCertPath)
		if err != nil {
			return nil, fmt.Errorf("error reading issuer cert from disk: %s", err)
		}

	} else {
		// create self signed root and issuer certs
		log.Info("root and issuer certs not found: generating self signed CA")
		issuerCreds, rootCertBytes, issuerCertBytes, err = c.generateRootAndIssuerCerts()
		if err != nil {
			return nil, fmt.Errorf("error generating trust root bundle: %s", err)
		}

		log.Info("self signed certs generated and persisted successfully")
	}

	// load trust anchors
	trustAnchors, err := certs.CertPoolFromPEM(rootCertBytes)
	if err != nil {
		return nil, fmt.Errorf("error parsing cert pool for trust anchors: %s", err)
	}

	return &trustRootBundle{
		issuerCreds:   issuerCreds,
		trustAnchors:  trustAnchors,
		trustDomain:   c.config.TrustDomain,
		rootCertPem:   rootCertBytes,
		issuerCertPem: issuerCertBytes,
	}, nil
}

// GenerateSidecarCertificate generates a keypair and returns a new certificate
func (c *defaultCA) GenerateSidecarCertificate(subject string) (*certs.Credentials, error) {
	pk, err := certs.GenerateECPrivateKey()
	if err != nil {
		return nil, err
	}

	csr := &x509.CertificateRequest{
		Subject:   pkix.Name{CommonName: subject},
		DNSNames:  []string{subject},
		PublicKey: &pk.PublicKey,
	}

	csrPem, err := x509.CreateCertificateRequest(rand.Reader, csr, pk)
	if err != nil {
		err = fmt.Errorf("error creating csr: %s", err)
		log.Error(err)
		return nil, err
	}

	signed, err := c.SignCSR(csrPem, subject, -1, false)
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

func (c *defaultCA) generateRootAndIssuerCerts() (*certs.Credentials, []byte, []byte, error) {
	rootKey, err := certs.GenerateECPrivateKey()
	if err != nil {
		return nil, nil, nil, err
	}
	rootCsr, err := csr.GenerateRootCertCSR(caOrg, caCommonName, &rootKey.PublicKey, selfSignedRootCertLifetime)
	if err != nil {
		return nil, nil, nil, err
	}

	rootCertBytes, err := x509.CreateCertificate(rand.Reader, rootCsr, rootCsr, &rootKey.PublicKey, rootKey)
	if err != nil {
		return nil, nil, nil, err
	}

	rootCertPem := pem.EncodeToMemory(&pem.Block{Type: certs.Certificate, Bytes: rootCertBytes})

	rootCert, err := x509.ParseCertificate(rootCertBytes)
	if err != nil {
		return nil, nil, nil, err
	}

	issuerKey, err := certs.GenerateECPrivateKey()
	if err != nil {
		return nil, nil, nil, err
	}

	issuerCsr, err := csr.GenerateIssuerCertCSR(caCommonName, &issuerKey.PublicKey, selfSignedRootCertLifetime)
	if err != nil {
		return nil, nil, nil, err
	}

	issuerCertBytes, err := x509.CreateCertificate(rand.Reader, issuerCsr, rootCert, &issuerKey.PublicKey, rootKey)
	if err != nil {
		return nil, nil, nil, err
	}

	issuerCertPem := pem.EncodeToMemory(&pem.Block{Type: certs.Certificate, Bytes: issuerCertBytes})

	encodedKey, err := x509.MarshalECPrivateKey(issuerKey)
	if err != nil {
		return nil, nil, nil, err
	}
	issuerKeyPem := pem.EncodeToMemory(&pem.Block{Type: certs.ECPrivateKey, Bytes: encodedKey})

	issuerCert, err := x509.ParseCertificate(issuerCertBytes)
	if err != nil {
		return nil, nil, nil, err
	}

	// store credentials so that next time sentry restarts it'll load normally
	err = certs.StoreCredentials(c.config, rootCertPem, issuerCertPem, issuerKeyPem)
	if err != nil {
		return nil, nil, nil, err
	}

	return &certs.Credentials{
		PrivateKey: &certs.PrivateKey{
			Type: certs.ECPrivateKey,
			Key:  issuerKey,
		},
		Certificate: issuerCert,
	}, rootCertPem, issuerCertPem, nil
}
