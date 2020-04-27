// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package grpc

import (
	"crypto/tls"
	"fmt"
	"sync"

	"github.com/dapr/dapr/pkg/channel"
	grpc_channel "github.com/dapr/dapr/pkg/channel/grpc"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/runtime/security"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const (
	// needed to load balance requests for target services with multiple endpoints, ie. multiple instances
	grpcServiceConfig = `{"loadBalancingPolicy":"round_robin"}`
)

// Manager is a wrapper around gRPC connection pooling
type Manager struct {
	AppClient      *grpc.ClientConn
	lock           *sync.Mutex
	connectionPool map[string]*grpc.ClientConn
	auth           security.Authenticator
	mode           modes.DaprMode
}

// NewGRPCManager returns a new grpc manager
func NewGRPCManager(mode modes.DaprMode) *Manager {
	return &Manager{
		lock:           &sync.Mutex{},
		connectionPool: map[string]*grpc.ClientConn{},
		mode:           mode,
	}
}

// SetAuthenticator sets the gRPC manager a tls authenticator context
func (g *Manager) SetAuthenticator(auth security.Authenticator) {
	g.auth = auth
}

// CreateLocalChannel creates a new gRPC AppChannel
func (g *Manager) CreateLocalChannel(port, maxConcurrency int, spec config.TracingSpec) (channel.AppChannel, error) {
	conn, err := g.GetGRPCConnection(fmt.Sprintf("127.0.0.1:%v", port), "", true, false)
	if err != nil {
		return nil, fmt.Errorf("error establishing connection to app grpc on port %v: %s", port, err)
	}

	g.AppClient = conn
	ch := grpc_channel.CreateLocalChannel(port, maxConcurrency, conn, spec)
	return ch, nil
}

// GetGRPCConnection returns a new grpc connection for a given address and inits one if doesn't exist
func (g *Manager) GetGRPCConnection(address, id string, skipTLS, recreateIfExists bool) (*grpc.ClientConn, error) {
	if val, ok := g.connectionPool[address]; ok && !recreateIfExists {
		return val, nil
	}

	g.lock.Lock()
	if val, ok := g.connectionPool[address]; ok && !recreateIfExists {
		g.lock.Unlock()
		return val, nil
	}

	opts := []grpc.DialOption{
		grpc.WithBlock(),
		grpc.WithStatsHandler(diag.DefaultGRPCMonitoring.ClientStatsHandler),
		grpc.WithDefaultServiceConfig(grpcServiceConfig),
	}

	if !skipTLS && g.auth != nil {
		signedCert := g.auth.GetCurrentSignedCert()
		cert, err := tls.X509KeyPair(signedCert.WorkloadCert, signedCert.PrivateKeyPem)
		if err != nil {
			return nil, fmt.Errorf("error generating x509 Key Pair: %s", err)
		}

		ta := credentials.NewTLS(&tls.Config{
			ServerName:   id,
			Certificates: []tls.Certificate{cert},
			RootCAs:      signedCert.TrustChain,
		})
		opts = append(opts, grpc.WithTransportCredentials(ta))
	} else {
		opts = append(opts, grpc.WithInsecure())
	}

	dialPrefix := GetDialAddressPrefix(g.mode)
	conn, err := grpc.Dial(dialPrefix+address, opts...)
	if err != nil {
		g.lock.Unlock()
		return nil, err
	}

	g.connectionPool[address] = conn
	g.lock.Unlock()

	return conn, nil
}
