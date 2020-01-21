package sentry

import (
	"context"

	"github.com/dapr/dapr/pkg/sentry/ca"
	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/dapr/dapr/pkg/sentry/server"
	log "github.com/sirupsen/logrus"
)

type CertificateAuthority interface {
	Run(context.Context, config.SentryConfig)
}

type sentry struct {
	server server.CAServer
}

// NewSentryCA returns a new Sentry Certificate Authority instance.
func NewSentryCA() CertificateAuthority {
	return &sentry{}
}

// Run loads the trust anchors and issuer certs, creates a new CA and runs the CA server.
func (s *sentry) Run(ctx context.Context, conf config.SentryConfig) {
	// Create CA
	certAuth, err := ca.NewCertificateAuthority(conf)
	if err != nil {
		log.Fatalf("error getting certificate authority: %s", err)
	}
	log.Info("certificate authority loaded")

	// Load the trust
	err = certAuth.LoadOrStoreTrustBundle()
	if err != nil {
		log.Fatalf("error loading trust root bundle: %s", err)
	}
	log.Infof("trust root bundle loaded. issuer cert expiry: %s", certAuth.GetCACertBundle().GetIssuerCertExpiry().String())

	// Run the CA server
	doneCh := make(chan struct{})
	s.server = server.NewCAServer(certAuth)

	go func() {
		select {
		case <-ctx.Done():
			log.Info("Sentry Certificate Authority is shutting down")
			s.server.Shutdown() // nolint: errcheck
		case <-doneCh:
		}
	}()

	log.Infof("Sentry Certificate Authority is running, protecting ya'll")
	err = s.server.Run(conf.Port, certAuth.GetCACertBundle())
	if err != nil {
		log.Fatalf("error starting gRPC server: %s", err)
	}

	close(doneCh)
}
