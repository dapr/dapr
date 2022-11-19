/*
Copyright 2021 The Dapr Authors
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

package grpc

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/dapr/dapr/pkg/channel"
	grpcChannel "github.com/dapr/dapr/pkg/channel/grpc"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/runtime/security"
)

const (
	// needed to load balance requests for target services with multiple endpoints, ie. multiple instances.
	grpcServiceConfig = `{"loadBalancingPolicy":"round_robin"}`
	dialTimeout       = time.Second * 30
)

// ClientConnCloser combines grpc.ClientConnInterface and io.Closer
// to cover the methods used from *grpc.ClientConn.
type ClientConnCloser interface {
	grpc.ClientConnInterface
	io.Closer
}

// Manager is a wrapper around gRPC connection pooling.
type Manager struct {
	AppClient      ClientConnCloser
	lock           *sync.RWMutex
	connectionPool *connectionPool
	auth           security.Authenticator
	mode           modes.DaprMode
}

// NewGRPCManager returns a new grpc manager.
func NewGRPCManager(mode modes.DaprMode) *Manager {
	return &Manager{
		lock:           &sync.RWMutex{},
		connectionPool: newConnectionPool(),
		mode:           mode,
	}
}

// SetAuthenticator sets the gRPC manager a tls authenticator context.
func (g *Manager) SetAuthenticator(auth security.Authenticator) {
	g.auth = auth
}

// CreateLocalChannel creates a new gRPC AppChannel.
func (g *Manager) CreateLocalChannel(port, maxConcurrency int, spec config.TracingSpec, sslEnabled bool, maxRequestBodySize int, readBufferSize int) (channel.AppChannel, error) {
	conn, _, err := g.GetGRPCConnection(context.TODO(), fmt.Sprintf("127.0.0.1:%v", port), "", "", true, false, sslEnabled)
	if err != nil {
		return nil, errors.Errorf("error establishing connection to app grpc on port %v: %s", port, err)
	}

	g.AppClient = conn
	ch := grpcChannel.CreateLocalChannel(port, maxConcurrency, conn, spec, maxRequestBodySize, readBufferSize)
	return ch, nil
}

// GetGRPCConnection returns a new grpc connection for a given address and inits one if doesn't exist.
func (g *Manager) GetGRPCConnection(parentCtx context.Context, address, id string, namespace string, skipTLS, recreateIfExists, sslEnabled bool, customOpts ...grpc.DialOption) (*grpc.ClientConn, func(), error) {
	releaseFactory := func(conn *grpc.ClientConn) func() {
		return func() {
			g.connectionPool.Release(conn)
		}
	}

	// share pooled connection
	if !recreateIfExists {
		g.lock.RLock()
		if conn, ok := g.connectionPool.Share(address); ok {
			g.lock.RUnlock()

			teardown := releaseFactory(conn)
			return conn, teardown, nil
		}
		g.lock.RUnlock()

		g.lock.RLock()
		// read the value once again, as a concurrent writer could create it
		if conn, ok := g.connectionPool.Share(address); ok {
			g.lock.RUnlock()

			teardown := releaseFactory(conn)
			return conn, teardown, nil
		}
		g.lock.RUnlock()
	}

	opts := []grpc.DialOption{
		grpc.WithDefaultServiceConfig(grpcServiceConfig),
	}

	if diag.DefaultGRPCMonitoring.IsEnabled() {
		opts = append(opts, grpc.WithUnaryInterceptor(diag.DefaultGRPCMonitoring.UnaryClientInterceptor()))
	}

	transportCredentialsAdded := false
	if !skipTLS && g.auth != nil {
		signedCert := g.auth.GetCurrentSignedCert()
		cert, err := tls.X509KeyPair(signedCert.WorkloadCert, signedCert.PrivateKeyPem)
		if err != nil {
			return nil, func() {}, errors.Errorf("error generating x509 Key Pair: %s", err)
		}

		var serverName string
		if id != "cluster.local" {
			serverName = fmt.Sprintf("%s.%s.svc.cluster.local", id, namespace)
		}

		//nolint:gosec
		ta := credentials.NewTLS(&tls.Config{
			ServerName:   serverName,
			Certificates: []tls.Certificate{cert},
			RootCAs:      signedCert.TrustChain,
		})
		opts = append(opts, grpc.WithTransportCredentials(ta))
		transportCredentialsAdded = true
	}

	dialPrefix := GetDialAddressPrefix(g.mode)
	if sslEnabled {
		//nolint:gosec
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			InsecureSkipVerify: true,
		})))
		transportCredentialsAdded = true
	}

	if !transportCredentialsAdded {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	opts = append(opts, customOpts...)

	ctx, cancel := context.WithTimeout(parentCtx, dialTimeout)
	conn, err := grpc.DialContext(ctx, dialPrefix+address, opts...)
	cancel()
	if err != nil {
		return nil, func() {}, err
	}

	teardown := releaseFactory(conn)
	g.lock.Lock()
	g.connectionPool.Register(address, conn)
	g.lock.Unlock()

	return conn, teardown, nil
}

type connectionPool struct {
	pool           map[string]*grpc.ClientConn
	referenceCount map[*grpc.ClientConn]int
	referenceLock  *sync.RWMutex
}

func newConnectionPool() *connectionPool {
	return &connectionPool{
		pool:           map[string]*grpc.ClientConn{},
		referenceCount: map[*grpc.ClientConn]int{},
		referenceLock:  &sync.RWMutex{},
	}
}

func (p *connectionPool) Register(address string, conn *grpc.ClientConn) {
	if oldConn, ok := p.pool[address]; ok {
		// oldConn is not used by pool anymore
		p.Release(oldConn)
	}

	p.pool[address] = conn
	// conn is used by caller and pool
	// NOTE: pool should also increment referenceCount not to close the pooled connection

	p.referenceLock.Lock()
	p.referenceCount[conn] = 2
	p.referenceLock.Unlock()
}

func (p *connectionPool) Share(address string) (*grpc.ClientConn, bool) {
	conn, ok := p.pool[address]
	if !ok {
		return nil, false
	}

	p.referenceLock.Lock()
	p.referenceCount[conn]++
	p.referenceLock.Unlock()
	return conn, true
}

func (p *connectionPool) Release(conn *grpc.ClientConn) {
	p.referenceLock.Lock()
	defer p.referenceLock.Unlock()

	if _, ok := p.referenceCount[conn]; !ok {
		return
	}

	p.referenceCount[conn]--

	// for concurrent use, connection is closed after all callers release it
	if p.referenceCount[conn] <= 0 {
		conn.Close()
		delete(p.referenceCount, conn)
	}
}
