/*
Copyright 2022 The Dapr Authors
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

package pluggable

import (
	"context"
	"fmt"

	"github.com/dapr/kit/logger"

	"github.com/pkg/errors"

	proto "github.com/dapr/dapr/pkg/proto/components/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var log = logger.NewLogger("pluggable-components-grpc-connector")

// GRPCClient is any client that supports common pluggable grpc operations.
type GRPCClient interface {
	// Ping is for liveness purposes.
	Ping(ctx context.Context, in *proto.PingRequest, opts ...grpc.CallOption) (*proto.PingResponse, error)
}

type GRPCConnectionDialer = func() (*grpc.ClientConn, error)

// GRPCConnector is a connector that uses underlying gRPC protocol for common operations.
type GRPCConnector[TClient GRPCClient] struct {
	// Context is the component shared context
	Context context.Context
	// Cancel is used for cancelling inflight requests
	Cancel context.CancelFunc
	// Client is the proto client.
	Client        TClient
	dialer        GRPCConnectionDialer
	conn          *grpc.ClientConn
	clientFactory func(grpc.ClientConnInterface) TClient
}

// socketDialer creates a dialer for the given socket.
func socketDialer(socket string, additionalOpts ...grpc.DialOption) GRPCConnectionDialer {
	return func() (*grpc.ClientConn, error) {
		return SocketDial(socket, additionalOpts...)
	}
}

// SocketDial creates a grpc connection using the given socket.
func SocketDial(socket string, additionalOpts ...grpc.DialOption) (*grpc.ClientConn, error) {
	udsSocket := fmt.Sprintf("unix://%s", socket)
	log.Debugf("using socket defined at '%s'", udsSocket)
	additionalOpts = append(additionalOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))

	grpcConn, err := grpc.Dial(udsSocket, additionalOpts...)
	if err != nil {
		return nil, errors.Wrapf(err, "unable to open GRPC connection using socket '%s'", udsSocket)
	}
	return grpcConn, nil
}

// Dial opens a grpcConnection and creates a new client instance.
func (g *GRPCConnector[TClient]) Dial() error {
	grpcConn, err := g.dialer()
	if err != nil {
		return errors.Wrapf(err, "unable to open GRPC connection using the dialer")
	}
	g.conn = grpcConn

	g.Client = g.clientFactory(grpcConn)

	return g.Ping()
}

// Ping pings the grpc component.
// It uses "WaitForReady" avoiding failing in transient failures.
func (g *GRPCConnector[TClient]) Ping() error {
	_, err := g.Client.Ping(g.Context, &proto.PingRequest{}, grpc.WaitForReady(true))
	return err
}

// Close closes the underlying gRPC connection and cancel all inflight requests.
func (g *GRPCConnector[TClient]) Close() error {
	g.Cancel()

	return g.conn.Close()
}

// NewGRPCConnectorWithDialer creates a new grpc connector for the given client factory and dialer.
func NewGRPCConnectorWithDialer[TClient GRPCClient](dialer GRPCConnectionDialer, factory func(grpc.ClientConnInterface) TClient) *GRPCConnector[TClient] {
	ctx, cancel := context.WithCancel(context.Background())

	return &GRPCConnector[TClient]{
		Context:       ctx,
		Cancel:        cancel,
		dialer:        dialer,
		clientFactory: factory,
	}
}

// NewGRPCConnector creates a new grpc connector for the given client factory and socket.
func NewGRPCConnector[TClient GRPCClient](socket string, factory func(grpc.ClientConnInterface) TClient) *GRPCConnector[TClient] {
	return NewGRPCConnectorWithDialer(socketDialer(socket), factory)
}
