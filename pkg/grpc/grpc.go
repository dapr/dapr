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
	"time"

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
	dialTimeout       = 30 * time.Second
	maxConnIdle       = 3 * time.Minute
)

// ConnCreatorFn is a function that returns a gRPC connection
type ConnCreatorFn = func() (grpc.ClientConnInterface, error)

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
	remoteConns       *RemoteConnectionPool
	auth              security.Authenticator
	mode              modes.DaprMode
	channelConfig     *AppChannelConfig
	localConn         *ConnectionPool
	localConnCreateFn ConnCreatorFn
	localConnLock     sync.RWMutex
}

// NewGRPCManager returns a new grpc manager.
func NewGRPCManager(mode modes.DaprMode, channelConfig *AppChannelConfig) *Manager {
	return &Manager{
		remoteConns:   NewRemoteConnectionPool(),
		mode:          mode,
		channelConfig: channelConfig,
		localConn:     NewConnectionPool(maxConnIdle, 1),
	}
}

// SetAuthenticator sets the gRPC manager a tls authenticator context.
func (g *Manager) SetAuthenticator(auth security.Authenticator) {
	g.auth = auth
}

// GetAppChannel returns a connection to the local channel.
// If there's no active connection to the app, it creates one.
func (g *Manager) GetAppChannel() (channel.AppChannel, error) {
	g.localConnLock.RLock()
	defer g.localConnLock.RUnlock()

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
	g.localConnLock.RLock()
	defer g.localConnLock.RUnlock()

	if g.localConnCreateFn != nil {
		return g.localConn.Get(g.localConnCreateFn)
	}
	return g.localConn.Get(g.defaultLocalConnCreateFn)
}

// CloseAppClient closes the active app client connections.
func (g *Manager) CloseAppClient() {
	g.localConn.DestroyAll()
}

// SetLocalConnCreateFn sets the function used to create local connections.
// It also destroys all existing local channel connections.
// Set fn to nil to reset to the built-in function.
func (g *Manager) SetLocalConnCreateFn(fn ConnCreatorFn) {
	g.localConnLock.Lock()
	defer g.localConnLock.Unlock()

	g.localConn.DestroyAll()
	g.localConnCreateFn = fn
}

// LocalConnCreatorFromNetConn returns a ConnCreatorFn (which can be passed to SetLocalConnCreateFn) which creates a new local connection from an existing net.Conn.
func (g *Manager) LocalConnCreatorFromNetConn(conn net.Conn) ConnCreatorFn {
	return func() (grpc.ClientConnInterface, error) {
		opts := make([]grpc.DialOption, 0, 4)

		if diag.DefaultGRPCMonitoring.IsEnabled() {
			opts = append(opts,
				grpc.WithUnaryInterceptor(diag.DefaultGRPCMonitoring.UnaryClientInterceptor()),
			)
		}

		if g.channelConfig.SSLEnabled {
			//nolint:gosec
			opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
				InsecureSkipVerify: true,
			})))
		} else {
			opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
		}

		opts = append(opts,
			grpc.WithBlock(),
			grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
				return conn, nil
			}),
		)

		ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
		defer cancel()
		grpcConn, err := grpc.DialContext(
			ctx,
			"0.0.0.0:0", // Target is 0.0.0.0:0 because we are using a custom dial function
			opts...,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create gRPC client connection on existing connection with client: %w", err)
		}

		return grpcConn, nil
	}
}

func (g *Manager) defaultLocalConnCreateFn() (grpc.ClientConnInterface, error) {
	conn, err := g.createLocalConnection(context.Background(), g.channelConfig.Port, g.channelConfig.SSLEnabled)
	if err != nil {
		return nil, fmt.Errorf("error establishing a grpc connection to app on port %v: %w", g.channelConfig.Port, err)
	}
	return conn, nil
}

func (g *Manager) createLocalConnection(parentCtx context.Context, port int, sslEnabled bool) (conn *grpc.ClientConn, err error) {
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
			return nil, fmt.Errorf("error loading x509 Key Pair: %w", err)
		}

		var serverName string
		if id != "cluster.local" {
			serverName = id + "." + namespace + ".svc.cluster.local"
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
			g.remoteConns.Destroy(address, conn)
		} else {
			g.remoteConns.Release(address, conn)
		}
	}
}

// StartCollector starts a background goroutine that periodically watches for expired connections and purges them.
func (g *Manager) StartCollector() {
	go func() {
		t := time.NewTicker(45 * time.Second)
		defer t.Stop()
		for range t.C {
			g.localConn.Purge()
			g.remoteConns.Purge()
		}
	}()
}

func nopTeardown(destroy bool) {
	// Nop
}
