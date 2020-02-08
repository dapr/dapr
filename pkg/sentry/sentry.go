package sentry

import (
	"context"
	"fmt"

	"github.com/dapr/dapr/pkg/sentry/ca"
	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/dapr/dapr/pkg/sentry/identity"
	"github.com/dapr/dapr/pkg/sentry/identity/kubernetes"
	"github.com/dapr/dapr/pkg/sentry/identity/selfhosted"
	k8s "github.com/dapr/dapr/pkg/sentry/kubernetes"
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

	// Create identity validator
	v, err := createValidator()
	if err != nil {
		log.Fatalf("error creating validator: %s", err)
	}
	log.Info("validator created")

	// Run the CA server
	s.doneCh = make(chan struct{})
	s.server = server.NewCAServer(certAuth, v)

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

func createValidator() (identity.Validator, error) {
	if config.IsKubernetesHosted() {
		// we're in Kubernetes, create client and init a new serviceaccount token validator
		kubeClient, err := k8s.GetClient()
		if err != nil {
			return nil, fmt.Errorf("failed to create kubernetes client: %s", err)
		}
		return kubernetes.NewValidator(kubeClient), nil
	}
	return selfhosted.NewValidator(), nil
}

func (s *sentry) Restart(ctx context.Context, conf config.SentryConfig) {
	s.server.Shutdown()
	close(s.doneCh)
	go s.Run(ctx, conf, nil)
}
