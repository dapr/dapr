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
	"google.golang.org/grpc"
	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.security")

type RequestFn func(ctx context.Context, der []byte) ([]*x509.Certificate, error)

// Handler implements middleware for client and server connection security.
type Handler interface {
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

	// AppID is the application ID of this workload.
	AppID string

	// MTLS is true if mTLS is enabled.
	MTLSEnabled bool

	// OverrideCertRequestSource is used to override where certificates are requested
	// from. Default to an implementation requesting from Sentry.
	OverrideCertRequestSource RequestFn

	// WriteSVIDoDir is the directory to write the X.509 SVID certificate private
	// key pair to. This is highly discouraged since it results in the private
	// key being written to file.
	WriteSVIDToDir *string
}

type provider struct {
	sec *security

	running atomic.Bool
	readyCh chan struct{}
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
		if len(opts.TrustAnchors) == 0 {
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
		readyCh: make(chan struct{}),
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
		log.Infof("fetching initial identity certificate from %s", p.sec.source.sentryAddress)
	}

	initialCert, err := p.sec.source.renewIdentityCertificate(ctx)
	if err != nil {
		return fmt.Errorf("failed to retrieve the initial identity certificate: %w", err)
	}

	diagnostics.DefaultMonitoring.MTLSInitCompleted()
	close(p.readyCh)
	log.Infof("Security is initialized successfully")
	p.sec.source.startRotation(ctx, p.sec.source.renewIdentityCertificate, initialCert)

	return nil
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

// GRPCServerOptionNoClientAuth returns a gRPC server option which instruments
// authentication of clients using the current trust anchors. Doesn't require
// clients to present a certificate.
func (s *security) GRPCServerOptionNoClientAuth() grpc.ServerOption {
	return grpc.Creds(
		grpccredentials.TLSServerCredentials(s.source),
	)
}

// CurrentTrustAnchors returns the current trust anchors for this Dapr
// installation.
func (s *security) CurrentTrustAnchors() ([]byte, error) {
	return s.source.trustAnchors.Marshal()
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
