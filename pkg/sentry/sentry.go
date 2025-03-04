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

	"github.com/dapr/dapr/pkg/healthz"
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

type Options struct {
	Config  config.Config
	Healthz healthz.Healthz
}

// CertificateAuthority is the interface for the Sentry Certificate Authority.
// Starts the Sentry gRPC server and signs workload certificates.
type CertificateAuthority interface {
	Start(context.Context) error
}

type sentry struct {
	runners *concurrency.RunnerManager
	running atomic.Bool
}

// New returns a new Sentry Certificate Authority instance.
func New(ctx context.Context, opts Options) (CertificateAuthority, error) {
	vals, err := buildValidators(opts)
	if err != nil {
		return nil, err
	}

	camngr, err := ca.New(ctx, opts.Config)
	if err != nil {
		return nil, fmt.Errorf("error creating CA: %w", err)
	}
	log.Info("CA certificate key pair ready")

	ns := security.CurrentNamespace()
	sec, err := security.New(ctx, security.Options{
		ControlPlaneTrustDomain: opts.Config.TrustDomain,
		ControlPlaneNamespace:   ns,
		AppID:                   "dapr-sentry",
		TrustAnchors:            camngr.TrustAnchors(),
		MTLSEnabled:             true,
		Healthz:                 opts.Healthz,
		Mode:                    opts.Config.Mode,
		// Override the request source to our in memory CA since _we_ are sentry!
		OverrideCertRequestFn: func(ctx context.Context, csrDER []byte) ([]*x509.Certificate, error) {
			csr, csrErr := x509.ParseCertificateRequest(csrDER)
			if csrErr != nil {
				monitoring.ServerCertIssueFailed("invalid_csr")
				return nil, csrErr
			}
			certs, csrErr := camngr.SignIdentity(ctx, &ca.SignRequest{
				PublicKey:          csr.PublicKey.(crypto.PublicKey),
				SignatureAlgorithm: csr.SignatureAlgorithm,
				TrustDomain:        opts.Config.TrustDomain,
				Namespace:          ns,
				AppID:              "dapr-sentry",
			})
			if csrErr != nil {
				monitoring.ServerCertIssueFailed("ca_error")
				return nil, csrErr
			}
			return certs, nil
		},
	})
	if err != nil {
		return nil, fmt.Errorf("error creating security: %s", err)
	}

	// Start all background processes
	runners := concurrency.NewRunnerManager(
		sec.Run,
		server.New(server.Options{
			Port:             opts.Config.Port,
			Security:         sec,
			Validators:       vals,
			DefaultValidator: opts.Config.DefaultValidator,
			CA:               camngr,
			Healthz:          opts.Healthz,
			ListenAddress:    opts.Config.ListenAddress,
		}).Start,
	)
	for name, val := range vals {
		log.Infof("Using validator '%s'", strings.ToLower(name.String()))
		if err := runners.Add(val.Start); err != nil {
			return nil, err
		}
	}

	return &sentry{
		runners: runners,
	}, nil
}

// Start the server.
func (s *sentry) Start(ctx context.Context) error {
	// If the server is already running, return an error
	if !s.running.CompareAndSwap(false, true) {
		return errors.New("CertificateAuthority server is already running")
	}

	if err := s.runners.Run(ctx); err != nil {
		return fmt.Errorf("error running Sentry: %v", err)
	}

	return nil
}

func buildValidators(opts Options) (map[sentryv1pb.SignCertificateRequest_TokenValidator]validator.Validator, error) {
	validators := make(map[sentryv1pb.SignCertificateRequest_TokenValidator]validator.Validator, len(opts.Config.Validators))

	for validatorID, val := range opts.Config.Validators {
		switch validatorID {
		case sentryv1pb.SignCertificateRequest_KUBERNETES:
			td, err := spiffeid.TrustDomainFromString(opts.Config.TrustDomain)
			if err != nil {
				return nil, err
			}
			sentryID, err := security.SentryID(td, security.CurrentNamespace())
			if err != nil {
				return nil, err
			}
			val, err := validatorKube.New(validatorKube.Options{
				RestConfig:     utils.GetConfig(),
				SentryID:       sentryID,
				ControlPlaneNS: security.CurrentNamespace(),
				Healthz:        opts.Healthz,
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
			td, err := spiffeid.TrustDomainFromString(opts.Config.TrustDomain)
			if err != nil {
				return nil, err
			}
			sentryID, err := security.SentryID(td, security.CurrentNamespace())
			if err != nil {
				return nil, err
			}
			obj := validatorJWKS.Options{
				SentryID: sentryID,
				Healthz:  opts.Healthz,
			}
			err = decodeOptions(&obj, val)
			if err != nil {
				return nil, fmt.Errorf("failed to decode validator options: %w", err)
			}
			val, err := validatorJWKS.New(obj)
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
