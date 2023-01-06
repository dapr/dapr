package sentry

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"sync"
	"time"

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
	Start(context.Context, config.SentryConfig) error
	Stop()
	Restart(context.Context, config.SentryConfig) error
}

type sentry struct {
	conf        config.SentryConfig
	ctx         context.Context
	cancel      context.CancelFunc
	server      server.CAServer
	restartLock sync.Mutex
	running     chan bool
	stopping    chan bool
}

// NewSentryCA returns a new Sentry Certificate Authority instance.
func NewSentryCA() CertificateAuthority {
	return &sentry{
		running: make(chan bool, 1),
	}
}

// Start the server in background.
func (s *sentry) Start(ctx context.Context, conf config.SentryConfig) error {
	// If the server is already running, return an error
	select {
	case s.running <- true:
	default:
		return errors.New("CertificateAuthority server is already running")
	}

	// Create the CA server
	s.conf = conf
	certAuth, v := s.createCAServer()

	// Start the server in background
	s.ctx, s.cancel = context.WithCancel(ctx)
	go s.run(certAuth, v)

	// Wait 100ms to ensure a clean startup
	time.Sleep(100 * time.Millisecond)

	return nil
}

// Loads the trust anchors and issuer certs, then creates a new CA.
func (s *sentry) createCAServer() (ca.CertificateAuthority, identity.Validator) {
	// Create CA
	certAuth, authorityErr := ca.NewCertificateAuthority(s.conf)
	if authorityErr != nil {
		log.Fatalf("error getting certificate authority: %s", authorityErr)
	}
	log.Info("certificate authority loaded")

	// Load the trust bundle
	trustStoreErr := certAuth.LoadOrStoreTrustBundle()
	if trustStoreErr != nil {
		log.Fatalf("error loading trust root bundle: %s", trustStoreErr)
	}
	certExpiry := certAuth.GetCACertBundle().GetIssuerCertExpiry()
	if certExpiry == nil {
		log.Fatalf("error loading trust root bundle: missing certificate expiry")
	} else {
		// Need to be in an else block for the linter
		log.Infof("trust root bundle loaded. issuer cert expiry: %s", certExpiry.String())
	}
	monitoring.IssuerCertExpiry(certExpiry)

	// Create identity validator
	v, validatorErr := s.createValidator()
	if validatorErr != nil {
		log.Fatalf("error creating validator: %s", validatorErr)
	}
	log.Info("validator created")

	return certAuth, v
}

// Runs the CA server.
// This method blocks until the server is shut down.
func (s *sentry) run(certAuth ca.CertificateAuthority, v identity.Validator) {
	s.server = server.NewCAServer(certAuth, v)

	// In background, watch for the root certificate's expiration
	go watchCertExpiry(s.ctx, certAuth)

	// Watch for context cancelation to stop the server
	go func() {
		<-s.ctx.Done()
		s.server.Shutdown()
		close(s.running)
		s.running = make(chan bool, 1)
		if s.stopping != nil {
			close(s.stopping)
		}
	}()

	// Start the server; this is a blocking call
	log.Infof("sentry certificate authority is running, protecting y'all")
	serverRunErr := s.server.Run(s.conf.Port, certAuth.GetCACertBundle())
	if serverRunErr != nil {
		log.Fatalf("error starting gRPC server: %s", serverRunErr)
	}
}

// Stop the server.
func (s *sentry) Stop() {
	log.Info("sentry certificate authority is shutting down")
	if s.cancel != nil {
		s.stopping = make(chan bool)
		s.cancel()
		<-s.stopping
		s.stopping = nil
	}
}

// Watches certificates' expiry and shows an error message when they're nearing expiration time.
// This is a blocking method that should be run in its own goroutine.
func watchCertExpiry(ctx context.Context, certAuth ca.CertificateAuthority) {
	log.Debug("starting root certificate expiration watcher")
	certExpiryCheckTicker := time.NewTicker(time.Hour)
	for {
		select {
		case <-certExpiryCheckTicker.C:
			caCrt := certAuth.GetCACertBundle().GetRootCertPem()
			block, _ := pem.Decode(caCrt)
			cert, certParseErr := x509.ParseCertificate(block.Bytes)
			if certParseErr != nil {
				log.Warn("could not determine Dapr root certificate expiration time")
				break
			}
			if cert.NotAfter.Before(time.Now().UTC()) {
				log.Warn("Dapr root certificate expiration warning: certificate has expired.")
				break
			}
			if (cert.NotAfter.Add(-30 * 24 * time.Hour)).Before(time.Now().UTC()) {
				expiryDurationHours := int(cert.NotAfter.Sub(time.Now().UTC()).Hours())
				log.Warnf("Dapr root certificate expiration warning: certificate expires in %d days and %d hours", expiryDurationHours/24, expiryDurationHours%24)
			} else {
				validity := cert.NotAfter.Sub(time.Now().UTC())
				log.Debugf("Dapr root certificate is still valid for %s", validity.String())
			}
		case <-ctx.Done():
			log.Debug("terminating root certificate expiration watcher")
			certExpiryCheckTicker.Stop()
			return
		}
	}
}

func (s *sentry) createValidator() (identity.Validator, error) {
	if config.IsKubernetesHosted() {
		// we're in Kubernetes, create client and init a new serviceaccount token validator
		kubeClient, err := k8s.GetClient()
		if err != nil {
			return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
		}

		// TODO: Remove once the NoDefaultTokenAudience feature is finalized
		noDefaultTokenAudience := false

		return kubernetes.NewValidator(kubeClient, s.conf.GetTokenAudiences(), noDefaultTokenAudience), nil
	}
	return selfhosted.NewValidator(), nil
}

func (s *sentry) Restart(ctx context.Context, conf config.SentryConfig) error {
	s.restartLock.Lock()
	defer s.restartLock.Unlock()
	log.Info("sentry certificate authority is restarting")
	s.Stop()
	// Wait 200ms to ensure a clean shutdown
	time.Sleep(200 * time.Millisecond)
	return s.Start(ctx, conf)
}
