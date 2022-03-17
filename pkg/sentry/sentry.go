package sentry

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"time"

	"github.com/pkg/errors"

	"github.com/dapr/dapr/pkg/sentry/ca"
	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/dapr/dapr/pkg/sentry/identity"
	"github.com/dapr/dapr/pkg/sentry/identity/kubernetes"
	"github.com/dapr/dapr/pkg/sentry/identity/selfhosted"
	k8s "github.com/dapr/dapr/pkg/sentry/kubernetes"
	"github.com/dapr/dapr/pkg/sentry/monitoring"
	"github.com/dapr/dapr/pkg/sentry/server"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.sentry")

type CertificateAuthority interface {
	Run(context.Context, config.SentryConfig, chan bool)
	Restart(ctx context.Context, conf config.SentryConfig)
}

type sentry struct {
	server    server.CAServer
	reloading bool
}

// NewSentryCA returns a new Sentry Certificate Authority instance.
func NewSentryCA() CertificateAuthority {
	return &sentry{}
}

// Run loads the trust anchors and issuer certs, creates a new CA and runs the CA server.
func (s *sentry) Run(ctx context.Context, conf config.SentryConfig, readyCh chan bool) {
	// Create CA
	certAuth, authorityErr := ca.NewCertificateAuthority(conf)
	if authorityErr != nil {
		log.Fatalf("error getting certificate authority: %s", authorityErr)
	}
	log.Info("certificate authority loaded")

	// Load the trust bundle
	trustStoreErr := certAuth.LoadOrStoreTrustBundle()
	if trustStoreErr != nil {
		log.Fatalf("error loading trust root bundle: %s", trustStoreErr)
	}
	log.Infof("trust root bundle loaded. issuer cert expiry: %s", certAuth.GetCACertBundle().GetIssuerCertExpiry().String())
	monitoring.IssuerCertExpiry(certAuth.GetCACertBundle().GetIssuerCertExpiry())

	// Create identity validator
	v, validatorErr := createValidator()
	if validatorErr != nil {
		log.Fatalf("error creating validator: %s", validatorErr)
	}
	log.Info("validator created")

	// Run the CA server
	s.server = server.NewCAServer(certAuth, v)

	certExpiryCheckTicker := time.NewTicker(time.Hour)
	go func() {
		for {
			select {
			case <-certExpiryCheckTicker.C:
				caCrt := certAuth.GetCACertBundle().GetRootCertPem()
				block, _ := pem.Decode(caCrt)
				cert, certParseErr := x509.ParseCertificate(block.Bytes)
				if certParseErr != nil {
					log.Warn("could not determine Dapr root certificate expiration time")
					continue
				}
				if cert.NotAfter.Before(time.Now().UTC()) {
					log.Warn("Dapr root certificate expiration warning: certificate has expired.")
					continue
				}
				if (cert.NotAfter.Add(-30 * 24 * time.Hour)).Before(time.Now().UTC()) {
					expiryDurationHours := int(cert.NotAfter.Sub(time.Now().UTC()).Hours())
					log.Warnf("Dapr root certificate expiration warning: certificate expires in %d days and %d hours", expiryDurationHours/24, expiryDurationHours%24)
				}
			case <-ctx.Done():
				log.Info("sentry certificate authority is shutting down")
				s.server.Shutdown() // nolint: errcheck
			}
		}
	}()

	if readyCh != nil {
		readyCh <- true
		s.reloading = false
	}

	log.Infof("sentry certificate authority is running, protecting ya'll")
	serverRunErr := s.server.Run(conf.Port, certAuth.GetCACertBundle())
	if serverRunErr != nil {
		log.Fatalf("error starting gRPC server: %s", serverRunErr)
	}
}

func createValidator() (identity.Validator, error) {
	if config.IsKubernetesHosted() {
		// we're in Kubernetes, create client and init a new serviceaccount token validator
		kubeClient, err := k8s.GetClient()
		if err != nil {
			return nil, errors.Wrap(err, "failed to create kubernetes client")
		}
		return kubernetes.NewValidator(kubeClient), nil
	}
	return selfhosted.NewValidator(), nil
}

func (s *sentry) Restart(ctx context.Context, conf config.SentryConfig) {
	if s.reloading {
		return
	}
	s.reloading = true

	s.server.Shutdown()
	go s.Run(ctx, conf, nil)
}
