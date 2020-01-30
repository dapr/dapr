package sentry

import (
	"context"

	"github.com/dapr/dapr/pkg/sentry/ca"
	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/dapr/dapr/pkg/sentry/server"
	log "github.com/sirupsen/logrus"
)

type CertificateAuthority interface {
	Run(context.Context, config.SentryConfig, chan bool)
	Restart(ctx context.Context, conf config.SentryConfig)
}

type sentry struct {
	server server.CAServer
	doneCh chan struct{}
}

// NewSentryCA returns a new Sentry Certificate Authority instance.
func NewSentryCA() CertificateAuthority {
	return &sentry{}
}

// Run loads the trust anchors and issuer certs, creates a new CA and runs the CA server.
func (s *sentry) Run(ctx context.Context, conf config.SentryConfig, readyCh chan bool) {
	// Create CA
	certAuth, err := ca.NewCertificateAuthority(conf)
	if err != nil {
		log.Fatalf("error getting certificate authority: %s", err)
	}
	log.Info("certificate authority loaded")

	// Load the trust bundle
	err = certAuth.LoadOrStoreTrustBundle()
	if err != nil {
		log.Fatalf("error loading trust root bundle: %s", err)
	}
	log.Infof("trust root bundle loaded. issuer cert expiry: %s", certAuth.GetCACertBundle().GetIssuerCertExpiry().String())

	// Run the CA server
	s.doneCh = make(chan struct{})
	s.server = server.NewCAServer(certAuth)

	go func() {
		select {
		case <-ctx.Done():
			log.Info("sentry certificate authority is shutting down")
			s.server.Shutdown() // nolint: errcheck
		case <-s.doneCh:
		}
	}()

	if readyCh != nil {
		readyCh <- true
	}

	log.Infof("sentry certificate authority is running, protecting ya'll")
	err = s.server.Run(conf.Port, certAuth.GetCACertBundle())
	if err != nil {
		log.Fatalf("error starting gRPC server: %s", err)
	}
}

func (s *sentry) Restart(ctx context.Context, conf config.SentryConfig) {
	s.server.Shutdown()
	close(s.doneCh)
	go s.Run(ctx, conf, nil)
}
