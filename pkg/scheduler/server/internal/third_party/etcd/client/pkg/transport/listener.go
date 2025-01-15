// Copyright 2015 The etcd Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// TODO: add modified by dapr add commit url from theirs (with commit hash)

package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"go.uber.org/zap"

	"github.com/dapr/dapr/pkg/security"
)

// NewListenerWithOpts creates a new creating a server-side listener which accepts listener options.
func NewListenerWithOpts(addr, scheme string, tlsinfo *TLSInfo, opts ...ListenerOption) (net.Listener, error) {
	if tlsinfo.Security != nil {
		opts = append(opts, WithTLSInfo(tlsinfo))
	}

	id, err := spiffeid.FromPath(tlsinfo.Security.ControlPlaneTrustDomain(), "/ns/"+tlsinfo.Security.ControlPlaneNamespace()+"/"+"dapr-scheduler")
	if err != nil {
		return nil, fmt.Errorf("failed to create SPIFFE ID for server: %w", err)
	}
	l, err := newListener(tlsinfo, addr, scheme, opts...)
	if err != nil {
		return l, err
	}

	return tlsinfo.Security.NetListenerID(l, id), err
}

func newListener(tlsinfo *TLSInfo, addr, scheme string, opts ...ListenerOption) (net.Listener, error) {
	if scheme == "unix" || scheme == "unixs" {
		// unix sockets via unix://laddr
		return NewUnixListener(addr)
	}

	lnOpts := newListenOpts(opts...)

	ln, err := newKeepAliveListener(&lnOpts.ListenConfig, addr, tlsinfo)
	if err != nil {
		return nil, err
	}

	lnOpts.Listener = ln
	return ln, err
}

func newKeepAliveListener(cfg *net.ListenConfig, addr string, tlsinfo *TLSInfo) (ln net.Listener, err error) {
	if cfg != nil {
		ln, err = cfg.Listen(context.TODO(), "tcp", addr)
	} else {
		ln, err = net.Listen("tcp", addr)
	}
	if err != nil {
		return
	}
	tlscfg, err := tlsinfo.ServerConfig()
	if err != nil {
		return
	}
	return NewKeepAliveListener(ln, "tcp", tlscfg)
}

type TLSInfo struct {
	// Logger logs TLS errors.
	// If nil, all logs are discarded.
	Logger *zap.Logger

	Security security.Handler

	// HandshakeFailure is optionally called when a connection fails to handshake. The
	// connection will be closed immediately afterward.
	HandshakeFailure func(*tls.Conn, error)
}

// ServerConfig generates a tls.Config object for use by an HTTP server.
func (info TLSInfo) ServerConfig() (*tls.Config, error) {
	cfg, err := info.Security.MTLSServerConfig(info.Security.ControlPlaneTrustDomain(), info.Security.ControlPlaneNamespace(), "dapr-scheduler")
	if err != nil {
		return nil, err
	}

	return cfg, nil
}

// ClientConfig generates a tls.Config object for use by an HTTP client.
func (info TLSInfo) ClientConfig() (*tls.Config, error) {
	cfg, err := info.Security.MTLSClientConfig(info.Security.ControlPlaneTrustDomain(), info.Security.ControlPlaneNamespace(), "dapr-scheduler")
	if err != nil {
		return nil, err
	}

	return cfg, nil
}

// IsClosedConnError returns true if the error is from closing listener, cmux.
// copied from golang.org/x/net/http2/http2.go
func IsClosedConnError(err error) bool {
	// 'use of closed network connection' (Go <=1.8)
	// 'use of closed file or network connection' (Go >1.8, internal/poll.ErrClosing)
	// 'mux: listener closed' (cmux.ErrListenerClosed)
	return err != nil && strings.Contains(err.Error(), "closed")
}
