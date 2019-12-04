// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package grpc

import (
	"fmt"
	"sync"

	"github.com/dapr/dapr/pkg/channel"
	grpc_channel "github.com/dapr/dapr/pkg/channel/grpc"
	"google.golang.org/grpc"
)

// Manager is a wrapper around gRPC connection pooling
type Manager struct {
	AppClient      *grpc.ClientConn
	lock           *sync.Mutex
	connectionPool map[string]*grpc.ClientConn
}

// NewGRPCManager returns a new grpc manager
func NewGRPCManager() *Manager {
	return &Manager{
		lock:           &sync.Mutex{},
		connectionPool: map[string]*grpc.ClientConn{},
	}
}

// CreateLocalChannel creates a new gRPC AppChannel
func (g *Manager) CreateLocalChannel(port, maxConcurrency int) (channel.AppChannel, error) {
	conn, err := g.GetGRPCConnection(fmt.Sprintf("127.0.0.1:%v", port))
	if err != nil {
		return nil, fmt.Errorf("error establishing connection to app grpc on port %v: %s", port, err)
	}

	g.AppClient = conn
	ch := grpc_channel.CreateLocalChannel(port, maxConcurrency, conn)
	return ch, nil
}

// GetGRPCConnection returns a new grpc connection for a given address and inits one if doesn't exist
func (g *Manager) GetGRPCConnection(address string) (*grpc.ClientConn, error) {
	if val, ok := g.connectionPool[address]; ok {
		return val, nil
	}

	g.lock.Lock()
	if val, ok := g.connectionPool[address]; ok {
		g.lock.Unlock()
		return val, nil
	}

	conn, err := grpc.Dial(address, grpc.WithInsecure())
	if err != nil {
		g.lock.Unlock()
		return nil, err
	}

	g.connectionPool[address] = conn
	g.lock.Unlock()

	return conn, nil
}
