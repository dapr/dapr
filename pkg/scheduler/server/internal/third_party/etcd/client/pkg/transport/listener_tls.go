// Copyright 2017 The etcd Authors
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

package transport

import (
	"crypto/tls"
	"fmt"
	"net"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
)

// tlsListener overrides a TLS listener, so it will reject client
// certificates with insufficient SAN credentials or CRL revoked
// certificates.
type tlsListener struct {
	net.Listener
	connc            chan net.Conn
	donec            chan struct{}
	err              error
	handshakeFailure func(*tls.Conn, error)
}

// NewTLSListener handshakes TLS connections and performs optional CRL checking.
func NewTLSListener(l net.Listener, tlsinfo *TLSInfo) (net.Listener, error) {
	return newTLSListener(l, tlsinfo)
}

func newTLSListener(l net.Listener, tlsinfo *TLSInfo) (net.Listener, error) {
	if tlsinfo == nil || tlsinfo.Security == nil {
		l.Close()
		return nil, fmt.Errorf("cannot listen on TLS for %s: KeyFile and CertFile are not presented", l.Addr().String())
	}

	hf := tlsinfo.HandshakeFailure
	if hf == nil {
		hf = func(*tls.Conn, error) {}
	}

	id, err := spiffeid.FromPath(tlsinfo.Security.ControlPlaneTrustDomain(), "/ns/"+tlsinfo.Security.ControlPlaneNamespace()+"/"+"dapr-scheduler")
	if err != nil {
		return nil, fmt.Errorf("failed to create SPIFFE ID for server: %w", err)
	}

	tlsList := tlsinfo.Security.NetListenerID(l, id)
	if tlsList == nil {
		return nil, fmt.Errorf("failed to create secure TLS listener")
	}
	tlsl := &tlsListener{
		Listener:         tlsList,
		connc:            make(chan net.Conn),
		donec:            make(chan struct{}),
		handshakeFailure: hf,
	}

	return tlsl, nil
}

func (l *tlsListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.connc:
		return conn, nil
	case <-l.donec:
		return nil, l.err
	}
}

func (l *tlsListener) Close() error {
	err := l.Listener.Close()
	<-l.donec
	return err
}
