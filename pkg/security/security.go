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

package security

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"sync/atomic"

	"github.com/spiffe/go-spiffe/v2/spiffegrpc/grpccredentials"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/concurrency"
	"github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/security/legacy"
	secpem "github.com/dapr/dapr/pkg/security/pem"
	"github.com/dapr/kit/fswatcher"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.security")

type RequestFn func(ctx context.Context, der []byte) ([]*x509.Certificate, error)

// Handler implements middleware for client and server connection security.
type Handler interface {
	GRPCServerOption() grpc.ServerOption
	GRPCServerOptionNoClientAuth() grpc.ServerOption
}

// Provider is the security provider.
type Provider interface {
	Start(context.Context) error
	Handler(context.Context) (Handler, error)
}

// Options are the options for the security authenticator.
type Options struct {
	// SentryAddress is the network address of the sentry server.
	SentryAddress string

	// ControlPlaneTrustDomain is the trust domain of the control plane
	// components.
	ControlPlaneTrustDomain string

	// ControlPlaneNamespace is the dapr namespace of the control plane
	// components.
	ControlPlaneNamespace string

	// TrustAnchors is the X.509 PEM encoded CA certificates for this Dapr
	// installation. Cannot be used with TrustAnchorsFile. TrustAnchorsFile is
	// preferred so changes to the file are automatically picked up.
	TrustAnchors []byte

	// TrustAnchorsFile is the path to the X.509 PEM encoded CA certificates for
	// this Dapr installation. Prefer this over TrustAnchors so changes to the
	// file are automatically picked up. Cannot be used with TrustAnchors.
	TrustAnchorsFile string

	// AppID is the application ID of this workload.
	AppID string

	// MTLS is true if mTLS is enabled.
	MTLSEnabled bool

	// OverrideCertRequestSource is used to override where certificates are requested
	// from. Default to an implementation requesting from Sentry.
	OverrideCertRequestSource RequestFn
}

type provider struct {
	sec *security

	running          atomic.Bool
	readyCh          chan struct{}
	trustAnchorsFile string
}

// security implements the Security interface.
type security struct {
	source *x509source
	mtls   bool

	controlPlaneNamespace string
}

func New(ctx context.Context, opts Options) (Provider, error) {
	if len(opts.ControlPlaneTrustDomain) == 0 {
		return nil, errors.New("control plane trust domain is required")
	}

	var source *x509source

	if opts.MTLSEnabled {
		if len(opts.TrustAnchors) > 0 && len(opts.TrustAnchorsFile) > 0 {
			return nil, errors.New("trust anchors cannot be specified in both TrustAnchors and TrustAnchorsFile")
		}

		if len(opts.TrustAnchors) == 0 && len(opts.TrustAnchorsFile) == 0 {
			return nil, errors.New("trust anchors are required")
		}

		var err error
		source, err = newX509Source(ctx, clock.RealClock{}, opts)
		if err != nil {
			return nil, err
		}
	} else {
		log.Warn("mTLS is disabled. Skipping certificate request and tls validation")
	}

	return &provider{
		readyCh:          make(chan struct{}),
		trustAnchorsFile: opts.TrustAnchorsFile,
		sec: &security{
			source:                source,
			mtls:                  opts.MTLSEnabled,
			controlPlaneNamespace: opts.ControlPlaneNamespace,
		},
	}, nil
}

// Start is a blocking function which starts the security provider, handling
// rotation of credentials.
func (p *provider) Start(ctx context.Context) error {
	if !p.running.CompareAndSwap(false, true) {
		return errors.New("security provider already started")
	}

	if !p.sec.mtls {
		close(p.readyCh)
		<-ctx.Done()
		return nil
	}

	if p.sec.source.requestFn == nil {
		p.sec.source.requestFn = p.sec.source.requestFromSentry
		log.Infof("Fetching initial identity certificate from %s", p.sec.source.sentryAddress)
	}

	initialCert, err := p.sec.source.renewIdentityCertificate(ctx)
	if err != nil {
		return fmt.Errorf("failed to retrieve the initial identity certificate: %w", err)
	}

	mngr := concurrency.NewRunnerManager(func(ctx context.Context) error {
		p.sec.source.startRotation(ctx, p.sec.source.renewIdentityCertificate, initialCert)
		return nil
	})

	if len(p.trustAnchorsFile) > 0 {
		caEvent := make(chan struct{})

		err = mngr.Add(
			func(ctx context.Context) error {
				log.Infof("Watching trust anchors file '%s' for changes", p.trustAnchorsFile)
				return fswatcher.Watch(ctx, p.trustAnchorsFile, caEvent)
			},
			func(ctx context.Context) error {
				for {
					select {
					case <-ctx.Done():
						return nil
					case <-caEvent:
						log.Info("Trust anchors file changed, reloading trust anchors")

						p.sec.source.lock.Lock()
						if uErr := p.sec.source.updateTrustAnchorFromFile(p.trustAnchorsFile); uErr != nil {
							log.Errorf("Failed to read trust anchors file '%s': %v", p.trustAnchorsFile, uErr)
						}
						p.sec.source.lock.Unlock()
					}
				}
			},
		)
		if err != nil {
			return err
		}
	}

	diagnostics.DefaultMonitoring.MTLSInitCompleted()
	close(p.readyCh)
	log.Infof("Security is initialized successfully")

	return mngr.Run(ctx)
}

// Handler returns a ready handler from the security provider. Blocks until
// the provider is ready.
func (p *provider) Handler(ctx context.Context) (Handler, error) {
	select {
	case <-p.readyCh:
		return p.sec, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// GRPCServerOption returns a gRPC server option which instruments
// authentication of clients using the current trust anchors.
func (s *security) GRPCServerOption() grpc.ServerOption {
	if !s.mtls {
		return grpc.Creds(insecure.NewCredentials())
	}

	// TODO: It would be better if we could give a subset of trust domains in
	// which this server authorizes.
	return grpc.Creds(
		credentials.NewTLS(legacy.NewServer(s.source, s.source, tlsconfig.AuthorizeAny())),
	)
}

// GRPCServerOptionNoClientAuth returns a gRPC server option which instruments
// authentication of clients using the current trust anchors. Doesn't require
// clients to present a certificate.
func (s *security) GRPCServerOptionNoClientAuth() grpc.ServerOption {
	return grpc.Creds(
		grpccredentials.TLSServerCredentials(s.source),
	)
}

// CurrentNamespace returns the namespace of this workload.
func CurrentNamespace() string {
	namespace, ok := os.LookupEnv("NAMESPACE")
	if !ok {
		return "default"
	}
	return namespace
}

// SentryID returns the SPIFFE ID of the sentry server.
func SentryID(sentryTrustDomain, sentryNamespace string) (spiffeid.ID, error) {
	td, err := spiffeid.TrustDomainFromString(sentryTrustDomain)
	if err != nil {
		return spiffeid.ID{}, fmt.Errorf("invalid trust domain: %w", err)
	}

	sentryID, err := spiffeid.FromPathf(td, "/ns/%s/dapr-sentry", sentryNamespace)
	if err != nil {
		return spiffeid.ID{}, fmt.Errorf("failed to parse sentry SPIFFE ID: %w", err)
	}

	return sentryID, nil
}

func (x *x509source) updateTrustAnchorFromFile(filepath string) error {
	rootPEMs, err := os.ReadFile(filepath)
	if err != nil {
		return fmt.Errorf("failed to read trust anchors file '%s': %w", filepath, err)
	}

	trustAnchorCerts, err := secpem.DecodePEMCertificates(rootPEMs)
	if err != nil {
		return fmt.Errorf("failed to decode trust anchors: %w", err)
	}

	x.trustAnchors.SetX509Authorities(trustAnchorCerts)

	return nil
}
