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
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffegrpc/grpccredentials"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/security/legacy"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/fswatcher"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.security")

type RequestFn func(ctx context.Context, der []byte) ([]*x509.Certificate, error)

// Handler implements middleware for client and server connection security.
//
//nolint:interfacebloat
type Handler interface {
	GRPCServerOptionMTLS() grpc.ServerOption
	GRPCServerOptionNoClientAuth() grpc.ServerOption
	GRPCDialOptionMTLSUnknownTrustDomain(ns, appID string) grpc.DialOption
	GRPCDialOptionMTLS(spiffeid.ID) grpc.DialOption

	TLSServerConfigNoClientAuth() *tls.Config
	NetListenerID(net.Listener, spiffeid.ID) net.Listener
	NetDialerID(context.Context, spiffeid.ID, time.Duration) func(network, addr string) (net.Conn, error)

	ControlPlaneTrustDomain() spiffeid.TrustDomain
	ControlPlaneNamespace() string
	CurrentTrustAnchors() ([]byte, error)

	MTLSEnabled() bool
	WatchTrustAnchors(context.Context, chan<- []byte)
}

// Provider is the security provider.
type Provider interface {
	Run(context.Context) error
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

	// Mode is the operation mode of this security instance (self-hosted or
	// Kubernetes).
	Mode modes.DaprMode
}

type provider struct {
	sec *security

	running          atomic.Bool
	readyCh          chan struct{}
	trustAnchorsFile string

	// fswatcherInterval is the interval at which the trust anchors file changes
	// are batched. Used for testing only, and 500ms otherwise.
	fswatcherInterval time.Duration
}

// security implements the Security interface.
type security struct {
	controlPlaneTrustDomain spiffeid.TrustDomain
	controlPlaneNamespace   string

	source *x509source
	mtls   bool
}

func New(ctx context.Context, opts Options) (Provider, error) {
	if len(opts.ControlPlaneTrustDomain) == 0 {
		return nil, errors.New("control plane trust domain is required")
	}

	td, err := spiffeid.TrustDomainFromString(opts.ControlPlaneTrustDomain)
	if err != nil {
		return nil, fmt.Errorf("invalid control plane trust domain: %w", err)
	}

	// Always request certificates from Sentry if mTLS is enabled or running in
	// Kubernetes. In Kubernetes, Daprd always communicates mTLS with the control
	// plane.
	var source *x509source
	if opts.MTLSEnabled || opts.Mode == modes.KubernetesMode {
		if len(opts.TrustAnchors) > 0 && len(opts.TrustAnchorsFile) > 0 {
			return nil, errors.New("trust anchors cannot be specified in both TrustAnchors and TrustAnchorsFile")
		}

		if len(opts.TrustAnchors) == 0 && len(opts.TrustAnchorsFile) == 0 {
			return nil, errors.New("trust anchors are required")
		}

		var err error
		source, err = newX509Source(ctx, clock.RealClock{}, td, opts)
		if err != nil {
			return nil, err
		}
	} else {
		log.Warn("mTLS is disabled. Skipping certificate request and tls validation")
	}

	return &provider{
		fswatcherInterval: time.Millisecond * 500,
		readyCh:           make(chan struct{}),
		trustAnchorsFile:  opts.TrustAnchorsFile,
		sec: &security{
			source:                  source,
			mtls:                    opts.MTLSEnabled,
			controlPlaneTrustDomain: td,
			controlPlaneNamespace:   opts.ControlPlaneNamespace,
		},
	}, nil
}

// Run is a blocking function which starts the security provider, handling
// rotation of credentials.
func (p *provider) Run(ctx context.Context) error {
	if !p.running.CompareAndSwap(false, true) {
		return errors.New("security provider already started")
	}

	// If the security source has not been initialized, then just wait to exit.
	if p.sec.source == nil {
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

		fs, err := fswatcher.New(fswatcher.Options{
			Targets:  []string{filepath.Dir(p.trustAnchorsFile)},
			Interval: &p.fswatcherInterval,
		})
		if err != nil {
			return err
		}

		err = mngr.Add(
			func(ctx context.Context) error {
				log.Infof("Watching trust anchors file '%s' for changes", p.trustAnchorsFile)
				return fs.Run(ctx, caEvent)
			},
			func(ctx context.Context) error {
				for {
					select {
					case <-ctx.Done():
						return nil
					case <-caEvent:
						log.Info("Trust anchors file changed, reloading trust anchors")

						if uErr := p.sec.source.updateTrustAnchorFromFile(ctx, p.trustAnchorsFile); uErr != nil {
							log.Errorf("Failed to read trust anchors file '%s': %v", p.trustAnchorsFile, uErr)
						}
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

// GRPCDialOptionMTLS returns a gRPC dial option which instruments client
// authentication using the current signed client certificate.
func (s *security) GRPCDialOptionMTLS(appID spiffeid.ID) grpc.DialOption {
	// If source has not been initialized, then just return an insecure dial
	// option. We don't check on `mtls` here as we still want to use mTLS with
	// control plane peers when running in Kubernetes mode even if mTLS is
	// disabled.
	if s.source == nil {
		return grpc.WithTransportCredentials(insecure.NewCredentials())
	}
	return grpc.WithTransportCredentials(credentials.NewTLS(
		legacy.NewDialClient(s.source, s.source, tlsconfig.AuthorizeID(appID)),
	))
}

// GRPCServerOptionMTLS returns a gRPC server option which instruments
// authentication of clients using the current trust anchors.
func (s *security) GRPCServerOptionMTLS() grpc.ServerOption {
	if !s.mtls {
		return grpc.Creds(insecure.NewCredentials())
	}

	return grpc.Creds(
		// TODO: It would be better if we could give a subset of trust domains in
		// which this server authorizes.
		grpccredentials.MTLSServerCredentials(s.source, s.source, tlsconfig.AuthorizeAny()),
	)
}

// GRPCServerOptionNoClientAuth returns a gRPC server option which instruments
// authentication of clients using the current trust anchors. Doesn't require
// clients to present a certificate.
func (s *security) GRPCServerOptionNoClientAuth() grpc.ServerOption {
	return grpc.Creds(grpccredentials.TLSServerCredentials(s.source))
}

// GRPCDialOptionMTLSUnknownTrustDomain returns a gRPC dial option which
// instruments client authentication using the current signed client
// certificate. Doesn't verify the servers trust domain, but does authorize the
// SPIFFE ID path.
// Used for clients which don't know the servers Trust Domain.
func (s *security) GRPCDialOptionMTLSUnknownTrustDomain(ns, appID string) grpc.DialOption {
	if !s.mtls {
		return grpc.WithTransportCredentials(insecure.NewCredentials())
	}

	expID := "/ns/" + ns + "/" + appID
	matcher := func(actual spiffeid.ID) error {
		if actual.Path() != expID {
			return fmt.Errorf("unexpected SPIFFE ID: %q", actual)
		}
		return nil
	}

	return grpc.WithTransportCredentials(credentials.NewTLS(
		legacy.NewDialClient(s.source, s.source, tlsconfig.AdaptMatcher(matcher)),
	))
}

// CurrentTrustAnchors returns the current trust anchors for this Dapr
// installation.
func (s *security) CurrentTrustAnchors() ([]byte, error) {
	if s.source == nil {
		return nil, nil
	}
	ta, err := s.source.trustAnchors.Marshal()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal trust anchors: %w", err)
	}

	return ta, nil
}

// ControlPlaneTrustDomain returns the trust domain of the control plane.
func (s *security) ControlPlaneTrustDomain() spiffeid.TrustDomain {
	return s.controlPlaneTrustDomain
}

// ControlPlaneNamespace returns the dapr namespace of the control plane.
func (s *security) ControlPlaneNamespace() string {
	return s.controlPlaneNamespace
}

// WatchTrustAnchors watches for changes to the trust domains and returns the
// PEM encoded trust domain roots.
// Returns when the given context is canceled.
func (s *security) WatchTrustAnchors(ctx context.Context, trustAnchors chan<- []byte) {
	sub := make(chan struct{})
	s.source.lock.Lock()
	s.source.trustAnchorSubscribers = append(s.source.trustAnchorSubscribers, sub)
	s.source.lock.Unlock()

	for {
		select {
		case <-ctx.Done():
			return
		case <-sub:
			caBundle, err := s.CurrentTrustAnchors()
			if err != nil {
				log.Errorf("failed to marshal trust anchors: %s", err)
				continue
			}

			select {
			case trustAnchors <- caBundle:
			case <-ctx.Done():
			}
		}
	}
}

// TLSServerConfigNoClientAuth returns a TLS server config which instruments
// using the current signed server certificate. Authorizes client certificate
// chains against the trust anchors.
func (s *security) TLSServerConfigNoClientAuth() *tls.Config {
	return tlsconfig.TLSServerConfig(s.source)
}

// NetListenerID returns a mTLS net listener which instruments using the
// current signed server certificate. Authorizes client matches against the
// given SPIFFE ID.
func (s *security) NetListenerID(lis net.Listener, id spiffeid.ID) net.Listener {
	if !s.mtls {
		return lis
	}
	return tls.NewListener(lis,
		tlsconfig.MTLSServerConfig(s.source, s.source, tlsconfig.AuthorizeID(id)),
	)
}

// NetDialerID returns a mTLS net dialer which instruments using the current
// signed client certificate. Authorizes server matches against the given
// SPIFFE ID.
func (s *security) NetDialerID(ctx context.Context, spiffeID spiffeid.ID, timeout time.Duration) func(string, string) (net.Conn, error) {
	if !s.mtls {
		return (&net.Dialer{Timeout: timeout, Cancel: ctx.Done()}).Dial
	}
	return (&tls.Dialer{
		NetDialer: (&net.Dialer{Timeout: timeout, Cancel: ctx.Done()}),
		Config:    tlsconfig.MTLSClientConfig(s.source, s.source, tlsconfig.AuthorizeID(spiffeID)),
	}).Dial
}

// MTLSEnabled returns true if mTLS is enabled.
func (s *security) MTLSEnabled() bool {
	return s.mtls
}

// CurrentNamespace returns the namespace of this workload.
func CurrentNamespace() string {
	namespace, ok := os.LookupEnv("NAMESPACE")
	if !ok {
		return "default"
	}
	return namespace
}

// CurrentNamespaceOrError returns the namespace of this workload. If current
// Namespace is not found, error.
func CurrentNamespaceOrError() (string, error) {
	namespace, ok := os.LookupEnv("NAMESPACE")
	if !ok {
		return "", errors.New("'NAMESPACE' environment variable not set")
	}

	if len(namespace) == 0 {
		return "", errors.New("'NAMESPACE' environment variable is empty")
	}

	return namespace, nil
}

// SentryID returns the SPIFFE ID of the sentry server.
func SentryID(sentryTrustDomain spiffeid.TrustDomain, sentryNamespace string) (spiffeid.ID, error) {
	sentryID, err := spiffeid.FromSegments(sentryTrustDomain, "ns", sentryNamespace, "dapr-sentry")
	if err != nil {
		return spiffeid.ID{}, fmt.Errorf("failed to parse sentry SPIFFE ID: %w", err)
	}

	return sentryID, nil
}
