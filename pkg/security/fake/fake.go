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
)

type Fake struct {
	grpcServerOptionFn             func() grpc.ServerOption
	grpcServerOptionNoClientAuthFn func() grpc.ServerOption
	grpcDialOptionFn               func(spiffeid.ID) grpc.DialOption

	tlsServerConfigNoClientAuth func() *tls.Config
	netListenerIDFn             func(net.Listener, spiffeid.ID) net.Listener
	netDialerIDFn               func(context.Context, spiffeid.ID, time.Duration) func(network, addr string) (net.Conn, error)

	currentTrustAnchorsFn func() ([]byte, error)
	watchTrustAnchorsFn   func(context.Context, chan<- []byte)
}

func New() *Fake {
	return &Fake{
		grpcServerOptionFn: func() grpc.ServerOption {
			return grpc.Creds(insecure.NewCredentials())
		},
		tlsServerConfigNoClientAuth: func() *tls.Config {
			return new(tls.Config)
		},
		grpcServerOptionNoClientAuthFn: func() grpc.ServerOption {
			return grpc.Creds(insecure.NewCredentials())
		},
		currentTrustAnchorsFn: func() ([]byte, error) {
			return []byte{}, nil
		},
		watchTrustAnchorsFn: func(context.Context, chan<- []byte) {
			return
		},
		grpcDialOptionFn: func(id spiffeid.ID) grpc.DialOption {
			return grpc.WithTransportCredentials(insecure.NewCredentials())
		},
		netListenerIDFn: func(l net.Listener, _ spiffeid.ID) net.Listener {
			return l
		},
		netDialerIDFn: func(context.Context, spiffeid.ID, time.Duration) func(network, addr string) (net.Conn, error) {
			return net.Dial
		},
	}
}

func (f *Fake) WithGRPCServerOptionNoClientAuthFn(fn func() grpc.ServerOption) *Fake {
	f.grpcServerOptionNoClientAuthFn = fn
	return f
}

func (f *Fake) WithGRPCServerOptionFn(fn func() grpc.ServerOption) *Fake {
	f.grpcServerOptionFn = fn
	return f
}

func (f *Fake) WithTLSServerConfigNoClientAuthFn(fn func() *tls.Config) *Fake {
	f.tlsServerConfigNoClientAuth = fn
	return f
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

func (f *Fake) GRPCServerOption() grpc.ServerOption {
	return f.grpcServerOptionFn()
}

func (f *Fake) TLSServerConfigNoClientAuth() *tls.Config {
	return f.tlsServerConfigNoClientAuth()
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

func (f *Fake) ControlPlaneNamespace() string {
	return "dapr-test"
}

func (f *Fake) ControlPlaneTrustDomain() spiffeid.TrustDomain {
	return spiffeid.RequireTrustDomainFromString("example.com")
}
