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
	"net"
	"strconv"
	"sync"
	"sync/atomic"
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
	"github.com/dapr/kit/ptr"
)

const (
	// needed to load balance requests for target services with multiple endpoints, ie. multiple instances.
	grpcServiceConfig = `{"loadBalancingPolicy":"round_robin"}`
	dialTimeout       = 30 * time.Second
	maxConnIdle       = 3 * time.Minute
)

// AppChannelConfig contains the configuration for the app channel.
type AppChannelConfig struct {
	Port                 int
	MaxConcurrency       int
	TracingSpec          config.TracingSpec
	SSLEnabled           bool
	MaxRequestBodySizeMB int
	ReadBufferSizeKB     int
}

// Manager is a wrapper around gRPC connection pooling.
type Manager struct {
	conn           grpc.ClientConnInterface
	connectionPool *connectionPool
	auth           security.Authenticator
	mode           modes.DaprMode
	channelConfig  *AppChannelConfig
	lock           *sync.RWMutex
}

// NewGRPCManager returns a new grpc manager.
func NewGRPCManager(mode modes.DaprMode, channelConfig *AppChannelConfig) *Manager {
	return &Manager{
		connectionPool: newConnectionPool(),
		mode:           mode,
		channelConfig:  channelConfig,
		lock:           &sync.RWMutex{},
	}
}

// SetAuthenticator sets the gRPC manager a tls authenticator context.
func (g *Manager) SetAuthenticator(auth security.Authenticator) {
	g.auth = auth
}

// GetAppChannel returns a connection to the local channel.
// If there's no active connection to the app, it creates one.
func (g *Manager) GetAppChannel() (channel.AppChannel, error) {
	conn, err := g.GetAppClient()
	if err != nil {
		return nil, err
	}

	ch := grpcChannel.CreateLocalChannel(
		g.channelConfig.Port,
		g.channelConfig.MaxConcurrency,
		conn.(*grpc.ClientConn),
		g.channelConfig.TracingSpec,
		g.channelConfig.MaxRequestBodySizeMB,
		g.channelConfig.ReadBufferSizeKB,
	)
	return ch, nil
}

// GetAppClient returns the gRPC connection to the local app.
// If there's no active connection to the app, it creates one.
func (g *Manager) GetAppClient() (grpc.ClientConnInterface, error) {
	// Return the existing connection if active
	g.lock.RLock()
	if g.conn != nil {
		g.lock.RUnlock()
		return g.conn, nil
	}
	g.lock.RUnlock()

	// Need to connect; get a write lock
	g.lock.Lock()
	defer g.lock.Unlock()

	// Check again if we have an existing connection after re-acquiring the lock
	if g.conn != nil {
		return g.conn, nil
	}

	// connectLocal implements a timeout, so we can use a background context here
	conn, err := g.connectLocal(context.Background(), g.channelConfig.Port, g.channelConfig.SSLEnabled)
	if err != nil {
		return nil, fmt.Errorf("error establishing a grpc connection to app on port %v: %w", g.channelConfig.Port, err)
	}
	g.conn = conn

	return g.conn, nil
}

// CloseAppClient closes the active app client connection.
func (g *Manager) CloseAppClient() error {
	g.lock.Lock()
	defer g.lock.Unlock()
	return g.doCloseAppClient()
}

// SetAppClient sets the connection to the app.
func (g *Manager) SetAppClient(conn grpc.ClientConnInterface) {
	g.lock.Lock()
	defer g.lock.Unlock()

	// Ignore errors here
	_ = g.doCloseAppClient()
	g.conn = conn
}

func (g *Manager) doCloseAppClient() error {
	if g.conn != nil {
		conn, ok := g.conn.(interface{ Close() error })
		g.conn = nil
		if ok {
			return conn.Close()
		}
	}
	return nil
}

func (g *Manager) connectLocal(parentCtx context.Context, port int, sslEnabled bool) (conn *grpc.ClientConn, err error) {
	opts := make([]grpc.DialOption, 0, 2)

	if diag.DefaultGRPCMonitoring.IsEnabled() {
		opts = append(opts,
			grpc.WithUnaryInterceptor(diag.DefaultGRPCMonitoring.UnaryClientInterceptor()),
		)
	}

	if sslEnabled {
		//nolint:gosec
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			InsecureSkipVerify: true,
		})))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	dialPrefix := GetDialAddressPrefix(g.mode)
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))

	ctx, cancel := context.WithTimeout(parentCtx, dialTimeout)
	defer cancel()
	return grpc.DialContext(ctx, dialPrefix+address, opts...)
}

// GetGRPCConnection returns a new grpc connection for a given address and inits one if doesn't exist.
func (g *Manager) GetGRPCConnection(
	parentCtx context.Context,
	address string,
	id string,
	namespace string,
	customOpts ...grpc.DialOption,
) (conn *grpc.ClientConn, teardown func(destroy bool), err error) {
	// Load or create a connection
	conn, err = g.connectionPool.Get(address, func() (*grpc.ClientConn, error) {
		return g.connectRemote(parentCtx, address, id, namespace, customOpts...)
	})
	if err != nil {
		return nil, nopTeardown, err
	}
	return conn, g.connTeardownFactory(address, conn), nil
}

func (g *Manager) connectRemote(
	parentCtx context.Context,
	address string,
	id string,
	namespace string,
	customOpts ...grpc.DialOption,
) (conn *grpc.ClientConn, err error) {
	opts := make([]grpc.DialOption, 0, 3+len(customOpts))
	opts = append(opts, grpc.WithDefaultServiceConfig(grpcServiceConfig))

	if diag.DefaultGRPCMonitoring.IsEnabled() {
		opts = append(opts,
			grpc.WithUnaryInterceptor(diag.DefaultGRPCMonitoring.UnaryClientInterceptor()),
		)
	}

	if g.auth != nil {
		signedCert := g.auth.GetCurrentSignedCert()
		var cert tls.Certificate
		cert, err = tls.X509KeyPair(signedCert.WorkloadCert, signedCert.PrivateKeyPem)
		if err != nil {
			return nil, errors.Errorf("error loading x509 Key Pair: %s", err)
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
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	opts = append(opts, customOpts...)

	dialPrefix := GetDialAddressPrefix(g.mode)

	ctx, cancel := context.WithTimeout(parentCtx, dialTimeout)
	conn, err = grpc.DialContext(ctx, dialPrefix+address, opts...)
	cancel()
	if err != nil {
		return nil, err
	}

	return conn, nil
}

func (g *Manager) connTeardownFactory(address string, conn *grpc.ClientConn) func(destroy bool) {
	return func(destroy bool) {
		if destroy {
			g.connectionPool.Destroy(address, conn)
		} else {
			g.connectionPool.Release(address, conn)
		}
	}
}

func nopTeardown(destroy bool) {
	// Nop
}

type connectionPoolItem struct {
	connections []*connectionPoolItemConnection
	lock        sync.RWMutex
}

type connectionPoolItemConnection struct {
	conn           *grpc.ClientConn
	referenceCount int32
	idleSince      atomic.Pointer[time.Time]
}

// Expired returns true if the connection has expired and should not be used
func (pic *connectionPoolItemConnection) Expired() bool {
	idle := pic.idleSince.Load()
	return idle != nil && time.Since(*idle) < maxConnIdle
}

// MarkIdle sets the current time as when the connection became idle
func (pic *connectionPoolItemConnection) MarkIdle() {
	pic.idleSince.Store(ptr.Of(time.Now()))
}

type connectionPool struct {
	pool *sync.Map
}

func newConnectionPool() *connectionPool {
	return &connectionPool{
		pool: &sync.Map{},
	}
}

// Get takes a connection from the pool or, if no connection exists, creates a new one using createFn, then stores it and returns it.
func (p *connectionPool) Get(address string, createFn func() (*grpc.ClientConn, error)) (conn *grpc.ClientConn, err error) {
	item := p.loadOrStoreItem(address)

	// Try getting an existing connection
	item.lock.RLock()
	conn = p.doShare(address, item)
	item.lock.RUnlock()
	if conn != nil {
		return conn, nil
	}

	// Couldn't find one, so acquire a write lock and create one
	item.lock.Lock()

	// Before we create a new one, make sure that no other goroutine has created one in the meanwhile
	conn = p.doShare(address, item)
	if conn != nil {
		item.lock.Unlock()
		return conn, nil
	}

	// Create a connection using createFn
	conn, err = createFn()
	if err != nil {
		item.lock.Unlock()
		return nil, err
	}
	p.doRegister(address, conn, item)

	item.lock.Unlock()

	return conn, nil
}

// Register a new connection.
func (p *connectionPool) Register(address string, conn *grpc.ClientConn) {
	item := p.loadOrStoreItem(address)

	item.lock.Lock()
	p.doRegister(address, conn, item)
	item.lock.Unlock()
}

func (p *connectionPool) doRegister(address string, conn *grpc.ClientConn, item *connectionPoolItem) {
	// Expand the slice if needed
	l := len(item.connections)
	if l > 0 {
		tmp := item.connections
		item.connections = make([]*connectionPoolItemConnection, l+1)
		copy(item.connections, tmp)
	} else {
		// Create a slice with length = 1 as most addresses will only have 1 active connection at a given time
		item.connections = make([]*connectionPoolItemConnection, 1)
	}
	store := &connectionPoolItemConnection{
		conn: conn,
	}
	store.MarkIdle()
	item.connections[l] = store
}

// Share takes a connection from the pool and increments its reference count.
// The result can be nil if no available connection can be found.
func (p *connectionPool) Share(address string) *grpc.ClientConn {
	itemI, ok := p.pool.Load(address)
	if !ok {
		return nil
	}
	item := itemI.(*connectionPoolItem)

	item.lock.RLock()
	conn := p.doShare(address, item)
	item.lock.RUnlock()

	return conn
}

func (p *connectionPool) doShare(address string, item *connectionPoolItem) *grpc.ClientConn {
	// If there's more than 1 connection, grab the first one whose reference count is less than 100 (assuming the default value for MaxConcurrentStreams
	for i := 0; i < len(item.connections); i++ {
		// Check if the connection is still valid first
		// First we check if the referenceCount is 0, and then we check if the connection has expired
		// This should be safe for concurrent use
		if atomic.LoadInt32(&item.connections[i].referenceCount) == 0 && item.connections[i].Expired() {
			continue
		}

		// Increment the reference counter to signal that we're using the connection
		count := atomic.AddInt32(&item.connections[i].referenceCount, 1)

		// If the reference count is less than 100, we can use this connection
		if count < 100 {
			return item.connections[i].conn
		}

		atomic.AddInt32(&item.connections[i].referenceCount, -1)
	}

	// Could not find a connection with less than 100 active streams, so return nil
	return nil
}

// Release is called when the method has finished using the connection.
// This decrements the reference counter for the connection.
func (p *connectionPool) Release(address string, conn *grpc.ClientConn) {
	itemI, ok := p.pool.Load(address)
	if !ok {
		return
	}
	item := itemI.(*connectionPoolItem)

	item.lock.RLock()
	for _, el := range item.connections {
		if el.conn == conn {
			count := atomic.AddInt32(&el.referenceCount, -1)
			if count <= 0 {
				el.MarkIdle()
			}
			item.lock.RUnlock()
			return
		}
	}
	item.lock.RUnlock()
}

// Destroy a connection, forcibly removing ti from the pool
func (p *connectionPool) Destroy(address string, conn *grpc.ClientConn) {
	itemI, ok := p.pool.Load(address)
	if !ok {
		return
	}

	item := itemI.(*connectionPoolItem)

	item.lock.Lock()
	n := 0
	for i := 0; i < len(item.connections); i++ {
		el := item.connections[i]
		if el.conn == conn {
			// Close and filter out the connection
			_ = el.conn.Close()
			continue
		}

		item.connections[n] = el
		n++
	}
	item.connections = item.connections[:n]
	item.lock.Unlock()
}

// Purge connections that have been idle for longer than maxConnIdle.
// Note that this method should not be called by multiple goroutines at the same time.
func (p *connectionPool) Purge() {
	p.pool.Range(func(keyI any, itemI any) bool {
		item := itemI.(*connectionPoolItem)

		item.lock.Lock()
		n := 0
		for i := 0; i < len(item.connections); i++ {
			el := item.connections[i]

			// If the connection has no use and the last usage was more than maxConnIdle ago, then close it and filter it out
			// Ok to load the reference count non-atomically because we have a write lock
			if el.referenceCount <= 0 && el.Expired() {
				_ = el.conn.Close()
				continue
			}

			item.connections[n] = el
			n++
		}
		item.connections = item.connections[:n]
		item.lock.Unlock()

		return true
	})
}

func (p *connectionPool) loadOrStoreItem(address string) *connectionPoolItem {
	itemI, ok := p.pool.Load(address)
	if !ok {
		// Use LoadOrStore here in case another goroutine is in the exact same spot
		itemI, _ = p.pool.LoadOrStore(address, &connectionPoolItem{})
	}
	return itemI.(*connectionPoolItem)
}
