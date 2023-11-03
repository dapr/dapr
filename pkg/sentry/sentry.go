/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package sentry

import (
	"context"
	"crypto"
	"crypto/x509"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/spiffe/go-spiffe/v2/spiffeid"

	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/dapr/dapr/pkg/sentry/monitoring"
	"github.com/dapr/dapr/pkg/sentry/server"
	"github.com/dapr/dapr/pkg/sentry/server/ca"
	"github.com/dapr/dapr/pkg/sentry/server/validator"
	validatorInsecure "github.com/dapr/dapr/pkg/sentry/server/validator/insecure"
	validatorJWKS "github.com/dapr/dapr/pkg/sentry/server/validator/jwks"
	validatorKube "github.com/dapr/dapr/pkg/sentry/server/validator/kubernetes"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.sentry")

// CertificateAuthority is the interface for the Sentry Certificate Authority.
// Starts the Sentry gRPC server and signs workload certificates.
type CertificateAuthority interface {
	Start(context.Context) error
}

type sentry struct {
	conf    config.Config
	running atomic.Bool
}

// New returns a new Sentry Certificate Authority instance.
func New(conf config.Config) CertificateAuthority {
	return &sentry{
		conf: conf,
	}
}

// Start the server in background.
func (s *sentry) Start(parentCtx context.Context) error {
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	// If the server is already running, return an error
	if !s.running.CompareAndSwap(false, true) {
		return errors.New("CertificateAuthority server is already running")
	}

	ns := security.CurrentNamespace()

	camngr, err := ca.New(ctx, s.conf)
	if err != nil {
		return fmt.Errorf("error creating CA: %w", err)
	}
	log.Info("CA certificate key pair ready")

	provider, err := security.New(ctx, security.Options{
		ControlPlaneTrustDomain: s.conf.TrustDomain,
		ControlPlaneNamespace:   ns,
		AppID:                   "dapr-sentry",
		TrustAnchors:            camngr.TrustAnchors(),
		MTLSEnabled:             true,
		// Override the request source to our in memory CA since _we_ are sentry!
		OverrideCertRequestSource: func(ctx context.Context, csrDER []byte) ([]*x509.Certificate, error) {
			csr, csrErr := x509.ParseCertificateRequest(csrDER)
			if csrErr != nil {
				monitoring.ServerCertIssueFailed("invalid_csr")
				return nil, csrErr
			}
			certs, csrErr := camngr.SignIdentity(ctx, &ca.SignRequest{
				PublicKey:          csr.PublicKey.(crypto.PublicKey),
				SignatureAlgorithm: csr.SignatureAlgorithm,
				TrustDomain:        s.conf.TrustDomain,
				Namespace:          ns,
				AppID:              "dapr-sentry",
				// TODO: @joshvanl: Remove in 1.13. Before 1.12, clients where not
				// authorizing the server based on the correct SPIFFE ID, and instead
				// matched on the DNS SAN `cluster.local`(!).
				DNS: []string{"cluster.local"},
			}, false)
			if csrErr != nil {
				monitoring.ServerCertIssueFailed("ca_error")
				return nil, csrErr
			}
			return certs, nil
		},
	})
	if err != nil {
		return fmt.Errorf("error creating security: %s", err)
	}

	vals, err := s.getValidators(ctx)
	if err != nil {
		return err
	}

	// Start all background processes
	runners := concurrency.NewRunnerManager(
		provider.Run,
		func(ctx context.Context) error {
			sec, secErr := provider.Handler(ctx)
			if secErr != nil {
				return secErr
			}

			return server.Start(ctx, server.Options{
				Port:             s.conf.Port,
				Security:         sec,
				Validators:       vals,
				DefaultValidator: s.conf.DefaultValidator,
				CA:               camngr,
			})
		},
	)
	for name, val := range vals {
		log.Infof("Using validator '%s'", strings.ToLower(name.String()))
		runners.Add(val.Start)
	}

	err = runners.Run(ctx)
	if err != nil {
		log.Fatalf("Error running Sentry: %v", err)
	}

	return nil
}

func (s *sentry) getValidators(ctx context.Context) (map[sentryv1pb.SignCertificateRequest_TokenValidator]validator.Validator, error) {
	validators := make(map[sentryv1pb.SignCertificateRequest_TokenValidator]validator.Validator, len(s.conf.Validators))

	for validatorID, opts := range s.conf.Validators {
		switch validatorID {
		case sentryv1pb.SignCertificateRequest_KUBERNETES:
			td, err := spiffeid.TrustDomainFromString(s.conf.TrustDomain)
			if err != nil {
				return nil, err
			}
			sentryID, err := security.SentryID(td, security.CurrentNamespace())
			if err != nil {
				return nil, err
			}
			val, err := validatorKube.New(ctx, validatorKube.Options{
				RestConfig:     utils.GetConfig(),
				SentryID:       sentryID,
				ControlPlaneNS: security.CurrentNamespace(),
			})
			if err != nil {
				return nil, err
			}
			log.Info("Adding validator 'kubernetes' with Sentry ID: " + sentryID.String())
			validators[validatorID] = val

		case sentryv1pb.SignCertificateRequest_INSECURE:
			log.Info("Adding validator 'insecure'")
			validators[validatorID] = validatorInsecure.New()

		case sentryv1pb.SignCertificateRequest_JWKS:
			td, err := spiffeid.TrustDomainFromString(s.conf.TrustDomain)
			if err != nil {
				return nil, err
			}
			sentryID, err := security.SentryID(td, security.CurrentNamespace())
			if err != nil {
				return nil, err
			}
			obj := validatorJWKS.Options{
				SentryID: sentryID,
			}
			err = decodeOptions(&obj, opts)
			if err != nil {
				return nil, fmt.Errorf("failed to decode validator options: %w", err)
			}
			val, err := validatorJWKS.New(ctx, obj)
			if err != nil {
				return nil, err
			}
			log.Info("Adding validator 'jwks' with Sentry ID: " + sentryID.String())
			validators[validatorID] = val
		}
	}

	if len(validators) == 0 {
		// Should never hit this, as the config is already sanitized
		return nil, errors.New("invalid validators")
	}

	return validators, nil
}
