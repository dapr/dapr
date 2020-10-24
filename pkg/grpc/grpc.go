// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package grpc

import (
	"context"
	"crypto/tls"
	"fmt"
	"sync"
	"time"

	"github.com/dapr/dapr/pkg/channel"
	grpc_channel "github.com/dapr/dapr/pkg/channel/grpc"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/runtime/security"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const (
	// needed to load balance requests for target services with multiple endpoints, ie. multiple instances
	grpcServiceConfig = `{"loadBalancingPolicy":"round_robin"}`
	dialTimeout       = time.Second * 30
)

// Manager is a wrapper around gRPC connection pooling
type Manager struct {
	AppClient      *grpc.ClientConn
	lock           *sync.RWMutex
	connectionPool map[string]*grpc.ClientConn
	auth           security.Authenticator
	mode           modes.DaprMode
}

// NewGRPCManager returns a new grpc manager
func NewGRPCManager(mode modes.DaprMode) *Manager {
	return &Manager{
		lock:           &sync.RWMutex{},
		connectionPool: map[string]*grpc.ClientConn{},
		mode:           mode,
	}
}

// SetAuthenticator sets the gRPC manager a tls authenticator context
func (g *Manager) SetAuthenticator(auth security.Authenticator) {
	g.auth = auth
}

// CreateLocalChannel creates a new gRPC AppChannel
func (g *Manager) CreateLocalChannel(port, maxConcurrency int, timeout time.Duration, spec config.TracingSpec, sslEnabled bool) (channel.AppChannel, error) {
	conn, err := g.GetGRPCConnection(fmt.Sprintf("127.0.0.1:%v", port), "", "", true, false, sslEnabled)
	if err != nil {
		return nil, errors.Errorf("error establishing connection to app grpc on port %v: %s", port, err)
	}

	g.AppClient = conn
	ch := grpc_channel.CreateLocalChannel(port, maxConcurrency, timeout, conn, spec)
	return ch, nil
}

// GetGRPCConnection returns a new grpc connection for a given address and inits one if doesn't exist
func (g *Manager) GetGRPCConnection(address, id string, namespace string, skipTLS, recreateIfExists, sslEnabled bool) (*grpc.ClientConn, error) {
	g.lock.RLock()
	if val, ok := g.connectionPool[address]; ok && !recreateIfExists {
		g.lock.RUnlock()
		return val, nil
	}
	g.lock.RUnlock()

	g.lock.Lock()
	defer g.lock.Unlock()
	// read the value once again, as a concurrent writer could create it
	if val, ok := g.connectionPool[address]; ok && !recreateIfExists {
		return val, nil
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
			return nil, errors.Errorf("error generating x509 Key Pair: %s", err)
		}

		var serverName string
		if id != "cluster.local" {
			serverName = fmt.Sprintf("%s.%s.svc.cluster.local", id, namespace)
		}

		// nolint:gosec
		ta := credentials.NewTLS(&tls.Config{
			ServerName:   serverName,
			Certificates: []tls.Certificate{cert},
			RootCAs:      signedCert.TrustChain,
		})
		opts = append(opts, grpc.WithTransportCredentials(ta))
		transportCredentialsAdded = true
	}

	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()

	dialPrefix := GetDialAddressPrefix(g.mode)
	if sslEnabled {
		// nolint:gosec
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			InsecureSkipVerify: true,
		})))
		transportCredentialsAdded = true
	}

	if !transportCredentialsAdded {
		opts = append(opts, grpc.WithInsecure())
	}

	conn, err := grpc.DialContext(ctx, dialPrefix+address, opts...)
	if err != nil {
		return nil, err
	}

	if c, ok := g.connectionPool[address]; ok {
		c.Close()
	}

	g.connectionPool[address] = conn

	return conn, nil
}
