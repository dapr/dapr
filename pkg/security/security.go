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
	"errors"
	"fmt"
	"net"
	"os"
	"sync/atomic"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffegrpc/grpccredentials"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/crypto/spiffe"
	spiffecontext "github.com/dapr/kit/crypto/spiffe/context"
	"github.com/dapr/kit/crypto/spiffe/trustanchors"
	"github.com/dapr/kit/crypto/spiffe/trustanchors/file"
	"github.com/dapr/kit/crypto/spiffe/trustanchors/static"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.security")

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

	MTLSClientConfig(spiffeid.ID) *tls.Config

	ControlPlaneTrustDomain() spiffeid.TrustDomain
	ControlPlaneNamespace() string
	CurrentTrustAnchors(context.Context) ([]byte, error)
	WithSVIDContext(context.Context) context.Context

	MTLSEnabled() bool
	ID() spiffeid.ID
	WatchTrustAnchors(context.Context, chan<- []byte)
	IdentityDir() *string
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
	TrustAnchorsFile *string

	// AppID is the application ID of this workload.
	AppID string

	// MTLS is true if mTLS is enabled.
	MTLSEnabled bool

	// OverrideCertRequestFn is used to override where certificates are requested
	// from. Default to an implementation requesting from Sentry.
	OverrideCertRequestFn spiffe.RequestSVIDFn

	// OverrideTrustAnchors is used to override where trust anchors are requested
	// from.
	OverrideTrustAnchors trustanchors.Interface

	// Mode is the operation mode of this security instance (self-hosted or
	// Kubernetes).
	Mode modes.DaprMode

	// SentryTokenFile is an optional file containing the token to authenticate
	// to sentry.
	SentryTokenFile *string

	// Healthz is used to signal the health of the security provider.
	Healthz healthz.Healthz

	// WriteIdentityToFile is used to write the identity private key and
	// certificate chain to file. The certificate chain and private key will be
	// written to the `tls.cert` and `tls.key` files respectively in the given
	// directory.
	WriteIdentityToFile *string

	// JSONWebKeySet is the JSON Web Key Set for this Dapr installation. Cannot be
	// used with JSONWebKeySetFile or TrustAnchorsFile.
	JSONWebKeySet []byte

	// JSONWebKeySetFile is the path to the JSON Web Key Set for this Dapr
	// installation. Prefer this over JSONWebKeySet so changes to the file are
	// automatically picked up. Cannot be used with JSONWebKeySet or TrustAnchors.
	JSONWebKeySetFile *string

	// JwtAudiences is the list of JWT audiences to be included in the certificate request.
	JwtAudiences []string
}

type provider struct {
	sec     *security
	running atomic.Bool
	htarget healthz.Target
	readyCh chan struct{}
}

// security implements the Security interface.
type security struct {
	controlPlaneTrustDomain spiffeid.TrustDomain
	controlPlaneNamespace   string

	trustAnchors trustanchors.Interface
	spiffe       *spiffe.SPIFFE
	id           spiffeid.ID
	mtls         bool

	identityDir      *string
	trustAnchorsFile *string
}

func New(ctx context.Context, opts Options) (Provider, error) {
	if err := checkUserIDGroupID(opts.Mode); err != nil {
		return nil, err
	}

	if len(opts.ControlPlaneTrustDomain) == 0 {
		return nil, errors.New("control plane trust domain is required")
	}

	htarget := opts.Healthz.AddTarget("security-provider")

	cptd, err := spiffeid.TrustDomainFromString(opts.ControlPlaneTrustDomain)
	if err != nil {
		return nil, fmt.Errorf("invalid control plane trust domain: %w", err)
	}

	// Always request certificates from Sentry if mTLS is enabled or running in
	// Kubernetes. In Kubernetes, Daprd always communicates mTLS with the control
	// plane.
	var spf *spiffe.SPIFFE
	var trustAnchors trustanchors.Interface
	if opts.MTLSEnabled || opts.Mode == modes.KubernetesMode {
		trustAnchors = opts.OverrideTrustAnchors
		if trustAnchors == nil {
			if len(opts.TrustAnchors) > 0 && opts.TrustAnchorsFile != nil {
				return nil, errors.New("trust anchors cannot be specified in both TrustAnchors and TrustAnchorsFile")
			}

			if len(opts.TrustAnchors) == 0 && opts.TrustAnchorsFile == nil {
				return nil, errors.New("trust anchors are required")
			}

			if len(opts.JSONWebKeySet) > 0 && opts.JSONWebKeySetFile != nil {
				return nil, errors.New("json web key set cannot be specified in both JSONWebKeySet and JSONWebKeySetFile")
			}

			if len(opts.TrustAnchors) > 0 && opts.JSONWebKeySetFile != nil {
				return nil, errors.New("json web key set file cannot be used with trust anchors")
			}

			if len(opts.JSONWebKeySet) > 0 && opts.TrustAnchorsFile != nil {
				return nil, errors.New("trust anchors file cannot be used with json web key set")
			}

			switch {
			case len(opts.TrustAnchors) > 0:
				trustAnchors, err = static.From(static.Options{
					Anchors: opts.TrustAnchors,
					Jwks:    opts.JSONWebKeySet,
				})
				if err != nil {
					return nil, err
				}
			case opts.TrustAnchorsFile != nil:
				trustAnchors = file.From(file.Options{
					Log:      log,
					CAPath:   *opts.TrustAnchorsFile,
					JwksPath: opts.JSONWebKeySetFile,
				})
			}
		}

		var reqFn spiffe.RequestSVIDFn
		if opts.OverrideCertRequestFn != nil {
			reqFn = opts.OverrideCertRequestFn
		} else {
			reqFn, err = newRequestFn(opts, trustAnchors, cptd)
			if err != nil {
				return nil, err
			}
		}
		spf = spiffe.New(spiffe.Options{
			Log:                 log,
			RequestSVIDFn:       reqFn,
			WriteIdentityToFile: opts.WriteIdentityToFile,
			TrustAnchors:        trustAnchors,
		})
	} else {
		log.Warn("mTLS is disabled. Skipping certificate request and tls validation")
	}

	return &provider{
		htarget: htarget,
		readyCh: make(chan struct{}),
		sec: &security{
			trustAnchors:            trustAnchors,
			spiffe:                  spf,
			mtls:                    opts.MTLSEnabled,
			controlPlaneTrustDomain: cptd,
			controlPlaneNamespace:   opts.ControlPlaneNamespace,
			identityDir:             opts.WriteIdentityToFile,
			trustAnchorsFile:        opts.TrustAnchorsFile,
		},
	}, nil
}

// Run is a blocking function which starts the security provider, handling
// rotation of credentials.
func (p *provider) Run(ctx context.Context) error {
	defer p.htarget.NotReady()
	if !p.running.CompareAndSwap(false, true) {
		return errors.New("security provider already started")
	}

	// If spiffe has not been initialized, then just wait to exit.
	if p.sec.spiffe == nil {
		close(p.readyCh)
		p.htarget.Ready()
		<-ctx.Done()
		return nil
	}

	return concurrency.NewRunnerManager(
		p.sec.spiffe.Run,
		p.sec.trustAnchors.Run,
		func(ctx context.Context) error {
			if err := p.sec.spiffe.Ready(ctx); err != nil {
				return err
			}
			id, err := p.sec.spiffe.X509SVIDSource().GetX509SVID()
			if err != nil {
				return err
			}
			p.sec.id = id.ID
			close(p.readyCh)
			diagnostics.DefaultMonitoring.MTLSInitCompleted()
			p.htarget.Ready()
			<-ctx.Done()
			return nil
		},
	).Run(ctx)
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
	if s.spiffe == nil {
		return grpc.WithTransportCredentials(insecure.NewCredentials())
	}

	return grpc.WithTransportCredentials(
		grpccredentials.MTLSClientCredentials(s.spiffe.X509SVIDSource(), s.trustAnchors, tlsconfig.AuthorizeID(appID)),
	)
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
		grpccredentials.MTLSServerCredentials(s.spiffe.X509SVIDSource(), s.trustAnchors, tlsconfig.AuthorizeAny()),
	)
}

// GRPCServerOptionNoClientAuth returns a gRPC server option which instruments
// authentication of clients using the current trust anchors. Doesn't require
// clients to present a certificate.
func (s *security) GRPCServerOptionNoClientAuth() grpc.ServerOption {
	return grpc.Creds(grpccredentials.TLSServerCredentials(s.spiffe.X509SVIDSource()))
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

	return grpc.WithTransportCredentials(
		grpccredentials.MTLSClientCredentials(s.spiffe.X509SVIDSource(), s.trustAnchors, tlsconfig.AdaptMatcher(matcher)),
	)
}

// CurrentTrustAnchors returns the current trust anchors for this Dapr
// installation.
func (s *security) CurrentTrustAnchors(ctx context.Context) ([]byte, error) {
	if s.spiffe == nil {
		return nil, nil
	}

	return s.trustAnchors.CurrentTrustAnchors(ctx)
}

// WatchTrustAnchors watches for changes to the trust domains and returns the
// PEM encoded trust domain roots.
// Returns when the given context is canceled.
func (s *security) WatchTrustAnchors(ctx context.Context, trustAnchors chan<- []byte) {
	s.trustAnchors.Watch(ctx, trustAnchors)
}

// ControlPlaneTrustDomain returns the trust domain of the control plane.
func (s *security) ControlPlaneTrustDomain() spiffeid.TrustDomain {
	return s.controlPlaneTrustDomain
}

// ControlPlaneNamespace returns the dapr namespace of the control plane.
func (s *security) ControlPlaneNamespace() string {
	return s.controlPlaneNamespace
}

// TLSServerConfigNoClientAuth returns a TLS server config which instruments
// using the current signed server certificate. Authorizes client certificate
// chains against the trust anchors.
func (s *security) TLSServerConfigNoClientAuth() *tls.Config {
	return tlsconfig.TLSServerConfig(s.spiffe.X509SVIDSource())
}

// NetListenerID returns a mTLS net listener which instruments using the
// current signed server certificate. Authorizes client matches against the
// given SPIFFE ID.
func (s *security) NetListenerID(lis net.Listener, id spiffeid.ID) net.Listener {
	if !s.mtls {
		return lis
	}
	return tls.NewListener(lis,
		tlsconfig.MTLSServerConfig(s.spiffe.X509SVIDSource(), s.trustAnchors, tlsconfig.AuthorizeID(id)),
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
		Config:    tlsconfig.MTLSClientConfig(s.spiffe.X509SVIDSource(), s.trustAnchors, tlsconfig.AuthorizeID(spiffeID)),
	}).Dial
}

// MTLSEnabled returns true if mTLS is enabled.
func (s *security) MTLSEnabled() bool {
	return s.mtls
}

// MTLSClientConfig returns a mTLS client config
func (s *security) MTLSClientConfig(id spiffeid.ID) *tls.Config {
	if !s.mtls {
		return nil
	}

	return tlsconfig.MTLSClientConfig(s.spiffe.X509SVIDSource(), s.trustAnchors, tlsconfig.AuthorizeID(id))
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

func (s *security) WithSVIDContext(ctx context.Context) context.Context {
	if s.spiffe == nil {
		return ctx
	}

	return spiffecontext.WithSpiffe(ctx, s.spiffe)
}

func (s *security) IdentityDir() *string {
	return s.identityDir
}

func (s *security) ID() spiffeid.ID {
	return s.id
}
