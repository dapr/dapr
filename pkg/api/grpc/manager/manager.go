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

package manager

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	grpcKeepalive "google.golang.org/grpc/keepalive"
	md "google.golang.org/grpc/metadata"

	"github.com/dapr/dapr/pkg/channel"
	grpcChannel "github.com/dapr/dapr/pkg/channel/grpc"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/security"
	securityConsts "github.com/dapr/dapr/pkg/security/consts"
)

const (
	// needed to load balance requests for target services with multiple endpoints, ie. multiple instances.
	grpcServiceConfig = `{"loadBalancingPolicy":"round_robin"}`
	dialTimeout       = 30 * time.Second
	maxConnIdle       = 3 * time.Minute
)

// ConnCreatorFn is a function that returns a gRPC connection
type ConnCreatorFn = func() (grpc.ClientConnInterface, error)

// AppChannelConfig contains the configuration for the app channel.
type AppChannelConfig struct {
	Port               int
	MaxConcurrency     int
	TracingSpec        config.TracingSpec
	EnableTLS          bool
	MaxRequestBodySize int // In bytes
	ReadBufferSize     int // In bytes
	BaseAddress        string
	AppAPIToken        string
}

// Manager is a wrapper around gRPC connection pooling.
type Manager struct {
	remoteConns   *RemoteConnectionPool
	mode          modes.DaprMode
	channelConfig *AppChannelConfig
	localConn     *ConnectionPool
	localConnLock sync.RWMutex
	sec           security.Handler
	wg            sync.WaitGroup
	closed        atomic.Bool
	closeCh       chan struct{}
}

// NewManager returns a new grpc manager.
func NewManager(sec security.Handler, mode modes.DaprMode, channelConfig *AppChannelConfig) *Manager {
	return &Manager{
		remoteConns:   NewRemoteConnectionPool(),
		mode:          mode,
		channelConfig: channelConfig,
		localConn:     NewConnectionPool(maxConnIdle, 1),
		sec:           sec,
		closeCh:       make(chan struct{}),
	}
}

// GetAppChannel returns a connection to the local channel.
// If there's no active connection to the app, it creates one.
func (g *Manager) GetAppChannel() (channel.AppChannel, error) {
	g.localConnLock.RLock()
	defer g.localConnLock.RUnlock()

	connFn := func() (*grpc.ClientConn, func(bool), error) {
		conn, teardown, err := g.GetAppClient()
		if err != nil {
			return nil, nil, err
		}
		return conn.(*grpc.ClientConn), teardown, nil
	}

	ch := grpcChannel.CreateLocalChannel(
		g.channelConfig.Port,
		g.channelConfig.MaxConcurrency,
		connFn,
		g.channelConfig.TracingSpec,
		g.channelConfig.MaxRequestBodySize,
		g.channelConfig.ReadBufferSize,
		g.channelConfig.BaseAddress,
		g.channelConfig.AppAPIToken,
	)
	return ch, nil
}

// GetAppClient returns the gRPC connection to the local app.
// If there's no active connection to the app, it creates one.
func (g *Manager) GetAppClient() (grpc.ClientConnInterface, func(bool), error) {
	conn, err := g.localConn.Get(g.defaultLocalConnCreateFn)
	if err != nil {
		return nil, nopTeardown, err
	}

	return conn, func(destroy bool) {
		if destroy {
			g.localConn.Destroy(conn)
		} else {
			g.localConn.Release(conn)
		}
	}, nil
}

// SetAppClientConn is used by tests to override the default connection
func (g *Manager) SetAppClientConn(conn grpc.ClientConnInterface) {
	g.localConn.Register(conn)
}

func (g *Manager) defaultLocalConnCreateFn() (grpc.ClientConnInterface, error) {
	conn, err := g.createLocalConnection(context.Background(), g.channelConfig.Port, g.channelConfig.EnableTLS)
	if err != nil {
		return nil, fmt.Errorf("error establishing a grpc connection to app on port %v: %w", g.channelConfig.Port, err)
	}
	return conn, nil
}

func (g *Manager) createLocalConnection(parentCtx context.Context, port int, enableTLS bool) (conn *grpc.ClientConn, err error) {
	opts := make([]grpc.DialOption, 0, 2)

	if diag.DefaultGRPCMonitoring.IsEnabled() {
		opts = append(opts,
			grpc.WithUnaryInterceptor(diag.DefaultGRPCMonitoring.UnaryClientInterceptor()),
		)
	}

	if enableTLS {
		//nolint:gosec
		tlsConfig := &tls.Config{InsecureSkipVerify: true}
		tlsConfig.MinVersion = channel.AppChannelMinTLSVersion
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	opts = append(opts, grpc.WithConnectParams(grpc.ConnectParams{
		Backoff:           backoff.DefaultConfig,
		MinConnectTimeout: 1 * time.Second,
	}))

	dialPrefix := GetDialAddressPrefix(g.mode)
	address := net.JoinHostPort(g.channelConfig.BaseAddress, strconv.Itoa(port))

	ctx, cancel := context.WithTimeout(parentCtx, dialTimeout)
	defer cancel()
	return grpc.DialContext(ctx, dialPrefix+address, opts...) //nolint:staticcheck
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
	var connI grpc.ClientConnInterface
	connI, err = g.remoteConns.Get(address, func() (grpc.ClientConnInterface, error) {
		return g.connectRemote(parentCtx, address, id, namespace, customOpts...)
	})
	if err != nil {
		return nil, nopTeardown, err
	}
	conn = connI.(*grpc.ClientConn)
	return conn, g.connTeardownFactory(address, conn), nil
}

func (g *Manager) connectRemote(
	parentCtx context.Context,
	address string,
	id string,
	namespace string,
	customOpts ...grpc.DialOption,
) (conn *grpc.ClientConn, err error) {
	opts := make([]grpc.DialOption, 0, 4+len(customOpts))
	opts = append(opts,
		grpc.WithDefaultServiceConfig(grpcServiceConfig),
		g.sec.GRPCDialOptionMTLSUnknownTrustDomain(namespace, id),
		grpc.WithKeepaliveParams(grpcKeepalive.ClientParameters{
			// Ping the server every 10s if there's no activity
			Time: 10 * time.Second,
			// Wait 5s for the ping ACK before assuming the connection is dead
			Timeout: 5 * time.Second,
			// Send pings even without active streams
			PermitWithoutStream: true,
		}),
	)

	if diag.DefaultGRPCMonitoring.IsEnabled() {
		opts = append(opts,
			grpc.WithUnaryInterceptor(diag.DefaultGRPCMonitoring.UnaryClientInterceptor()),
		)
	}

	opts = append(opts, customOpts...)

	dialPrefix := GetDialAddressPrefix(g.mode)

	ctx, cancel := context.WithTimeout(parentCtx, dialTimeout)
	defer cancel()
	conn, err = grpc.DialContext(ctx, dialPrefix+address, opts...) //nolint:staticcheck
	if err != nil {
		return nil, err
	}

	return conn, nil
}

func (g *Manager) connTeardownFactory(address string, conn *grpc.ClientConn) func(destroy bool) {
	return func(destroy bool) {
		if destroy {
			g.remoteConns.Destroy(address, conn)
		} else {
			g.remoteConns.Release(address, conn)
		}
	}
}

// StartCollector starts a background goroutine that periodically watches for expired connections and purges them.
func (g *Manager) StartCollector() {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()

		t := time.NewTicker(45 * time.Second)
		defer t.Stop()

		for {
			select {
			case <-g.closeCh:
				g.localConn.DestroyAll()
				g.remoteConns.Purge()
				return
			case <-t.C:
				g.localConn.Purge()
				g.remoteConns.Purge()
			}
		}
	}()
}

func (g *Manager) Close() error {
	defer g.wg.Wait()
	if g.closed.CompareAndSwap(false, true) {
		close(g.closeCh)
	}

	return nil
}

func nopTeardown(destroy bool) {
	// Nop
}

// AddAppTokenToContext appends the app API token to outgoing gRPC context.
func (g *Manager) AddAppTokenToContext(ctx context.Context) context.Context {
	if g == nil || g.channelConfig == nil {
		return ctx
	}
	if g.channelConfig.AppAPIToken != "" {
		return md.AppendToOutgoingContext(ctx, securityConsts.APITokenHeader, g.channelConfig.AppAPIToken)
	}
	return ctx
}
