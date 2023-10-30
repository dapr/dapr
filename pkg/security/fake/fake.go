//go:build unit
// +build unit

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

package fake

import (
	"context"
	"crypto/tls"
	"net"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/dapr/dapr/pkg/security"
)

type Fake struct {
	controlPlaneTrustDomainFn func() spiffeid.TrustDomain
	controlPlaneNamespaceFn   func() string
	currentTrustAnchorsFn     func() ([]byte, error)
	watchTrustAnchorsFn       func(context.Context, chan<- []byte)
	mtls                      bool

	tlsServerConfigMTLSFn               func(spiffeid.TrustDomain) (*tls.Config, error)
	tlsServerConfigNoClientAuthFn       func() *tls.Config
	tlsServerConfigNoClientAuthOptionFn func(*tls.Config)
	netListenerIDFn                     func(net.Listener, spiffeid.ID) net.Listener
	netDialerIDFn                       func(context.Context, spiffeid.ID, time.Duration) func(network, addr string) (net.Conn, error)

	grpcDialOptionFn                   func(spiffeid.ID) grpc.DialOption
	grpcDialOptionUnknownTrustDomainFn func(ns, appID string) grpc.DialOption
	grpcServerOptionMTLSFn             func() grpc.ServerOption
	grpcServerOptionNoClientAuthFn     func() grpc.ServerOption
}

func New() *Fake {
	return &Fake{
		controlPlaneTrustDomainFn: func() spiffeid.TrustDomain {
			return spiffeid.RequireTrustDomainFromString("example.org")
		},
		controlPlaneNamespaceFn: func() string {
			return "dapr-test"
		},
		tlsServerConfigMTLSFn: func(spiffeid.TrustDomain) (*tls.Config, error) {
			return new(tls.Config), nil
		},
		tlsServerConfigNoClientAuthFn: func() *tls.Config {
			return new(tls.Config)
		},
		tlsServerConfigNoClientAuthOptionFn: func(*tls.Config) {},
		grpcDialOptionFn: func(spiffeid.ID) grpc.DialOption {
			return grpc.WithTransportCredentials(insecure.NewCredentials())
		},
		grpcDialOptionUnknownTrustDomainFn: func(ns, appID string) grpc.DialOption {
			return grpc.WithTransportCredentials(insecure.NewCredentials())
		},
		grpcServerOptionMTLSFn: func() grpc.ServerOption {
			return grpc.Creds(nil)
		},
		grpcServerOptionNoClientAuthFn: func() grpc.ServerOption {
			return grpc.Creds(nil)
		},
		currentTrustAnchorsFn: func() ([]byte, error) {
			return []byte{}, nil
		},
		watchTrustAnchorsFn: func(context.Context, chan<- []byte) {
			return
		},
		netListenerIDFn: func(l net.Listener, _ spiffeid.ID) net.Listener {
			return l
		},
		netDialerIDFn: func(context.Context, spiffeid.ID, time.Duration) func(network, addr string) (net.Conn, error) {
			return net.Dial
		},
		mtls: false,
	}
}

func (f *Fake) WithControlPlaneTrustDomainFn(fn func() spiffeid.TrustDomain) *Fake {
	f.controlPlaneTrustDomainFn = fn
	return f
}

func (f *Fake) WithControlPlaneNamespaceFn(fn func() string) *Fake {
	f.controlPlaneNamespaceFn = fn
	return f
}

func (f *Fake) WithTLSServerConfigMTLSFn(fn func(spiffeid.TrustDomain) (*tls.Config, error)) *Fake {
	f.tlsServerConfigMTLSFn = fn
	return f
}

func (f *Fake) WithTLSServerConfigNoClientAuthFn(fn func() *tls.Config) *Fake {
	f.tlsServerConfigNoClientAuthFn = fn
	return f
}

func (f *Fake) WithTLSServerConfigNoClientAuthOptionFn(fn func(*tls.Config)) *Fake {
	f.tlsServerConfigNoClientAuthOptionFn = fn
	return f
}

func (f *Fake) WithGRPCDialOptionMTLSFn(fn func(spiffeid.ID) grpc.DialOption) *Fake {
	f.grpcDialOptionFn = fn
	return f
}

func (f *Fake) WithGRPCDialOptionMTLSUnknownTrustDomainFn(fn func(ns, appID string) grpc.DialOption) *Fake {
	f.grpcDialOptionUnknownTrustDomainFn = fn
	return f
}

func (f *Fake) WithGRPCServerOptionMTLSFn(fn func() grpc.ServerOption) *Fake {
	f.grpcServerOptionMTLSFn = fn
	return f
}

func (f *Fake) WithGRPCServerOptionNoClientAuthFn(fn func() grpc.ServerOption) *Fake {
	f.grpcServerOptionNoClientAuthFn = fn
	return f
}

func (f *Fake) WithMTLSEnabled(mtls bool) *Fake {
	f.mtls = mtls
	return f
}

func (f *Fake) ControlPlaneTrustDomain() spiffeid.TrustDomain {
	return f.controlPlaneTrustDomainFn()
}

func (f *Fake) ControlPlaneNamespace() string {
	return f.controlPlaneNamespaceFn()
}

func (f *Fake) TLSServerConfigMTLS(td spiffeid.TrustDomain) (*tls.Config, error) {
	return f.tlsServerConfigMTLSFn(td)
}

func (f *Fake) TLSServerConfigNoClientAuth() *tls.Config {
	return f.tlsServerConfigNoClientAuthFn()
}

func (f *Fake) TLSServerConfigNoClientAuthOption(cfg *tls.Config) {
	f.tlsServerConfigNoClientAuthOptionFn(cfg)
}

func (f *Fake) GRPCDialOptionMTLS(id spiffeid.ID) grpc.DialOption {
	return f.grpcDialOptionFn(id)
}

func (f *Fake) GRPCDialOptionMTLSUnknownTrustDomain(ns, appID string) grpc.DialOption {
	return f.grpcDialOptionUnknownTrustDomainFn(ns, appID)
}

func (f *Fake) GRPCServerOptionMTLS() grpc.ServerOption {
	return f.grpcServerOptionMTLSFn()
}

func (f *Fake) WithCurrentTrustAnchorsFn(fn func() ([]byte, error)) *Fake {
	f.currentTrustAnchorsFn = fn
	return f
}

func (f *Fake) WithWatchTrustAnchorsFn(fn func(context.Context, chan<- []byte)) *Fake {
	f.watchTrustAnchorsFn = fn
	return f
}

func (f *Fake) WithGRPCDialOptionFn(fn func(spiffeid.ID) grpc.DialOption) *Fake {
	f.grpcDialOptionFn = fn
	return f
}

func (f *Fake) WithNetListenerIDFn(fn func(net.Listener, spiffeid.ID) net.Listener) *Fake {
	f.netListenerIDFn = fn
	return f
}

func (f *Fake) WithNetDialerIDFn(fn func(context.Context, spiffeid.ID, time.Duration) func(network, addr string) (net.Conn, error)) *Fake {
	f.netDialerIDFn = fn
	return f
}

func (f *Fake) GRPCServerOptionNoClientAuth() grpc.ServerOption {
	return f.grpcServerOptionNoClientAuthFn()
}

func (f *Fake) CurrentTrustAnchors() ([]byte, error) {
	return f.currentTrustAnchorsFn()
}

func (f *Fake) WatchTrustAnchors(ctx context.Context, ch chan<- []byte) {
	f.watchTrustAnchorsFn(ctx, ch)
}

func (f *Fake) GRPCDialOption(id spiffeid.ID) grpc.DialOption {
	return f.grpcDialOptionFn(id)
}

func (f *Fake) NetListenerID(l net.Listener, id spiffeid.ID) net.Listener {
	return f.netListenerIDFn(l, id)
}

func (f *Fake) NetDialerID(ctx context.Context, id spiffeid.ID, timeout time.Duration) func(network, addr string) (net.Conn, error) {
	return f.netDialerIDFn(ctx, id, timeout)
}

func (f *Fake) MTLSEnabled() bool {
	return f.mtls
}

func (f *Fake) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (f *Fake) Handler(context.Context) (security.Handler, error) {
	return f, nil
}
