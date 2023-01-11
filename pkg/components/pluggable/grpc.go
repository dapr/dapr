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

	proto "github.com/dapr/dapr/pkg/proto/components/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var log = logger.NewLogger("pluggable-components-grpc-connector")

// GRPCClient is any client that supports common pluggable grpc operations.
type GRPCClient interface {
	// Ping is for liveness purposes.
	Ping(ctx context.Context, in *proto.PingRequest, opts ...grpc.CallOption) (*proto.PingResponse, error)
}

// NewConverterFunc returns a function that maps from any error to a business error.
// if the error is unknown it is kept as is, otherwise a converter function will be used.
func NewConverterFunc(errorsConverters MethodErrorConverter) func(error) error {
	return func(err error) error {
		s, ok := status.FromError(err)
		if !ok {
			return err
		}
		convert, ok := errorsConverters[s.Code()]
		if !ok {
			return err
		}
		return convert(*s)
	}
}

type GRPCConnectionDialer func(ctx context.Context, name string, opts ...grpc.DialOption) (*grpc.ClientConn, error)

// WithOptions returns a new connection dialer that adds the new options to it.
func (g GRPCConnectionDialer) WithOptions(newOpts ...grpc.DialOption) GRPCConnectionDialer {
	return func(ctx context.Context, name string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
		return g(ctx, name, append(opts, newOpts...)...)
	}
}

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

// metadataInstanceID is used to differentiate between multiples instance of the same component.
const metadataInstanceID = "x-component-instance"

// instanceIDStreamInterceptor returns a grpc client unary interceptor that adds the instanceID on outgoing metadata.
// instanceID is used for multiplexing connection if the component supports it.
func instanceIDUnaryInterceptor(instanceID string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		return invoker(metadata.AppendToOutgoingContext(ctx, metadataInstanceID, instanceID), method, req, reply, cc, opts...)
	}
}

// instanceIDStreamInterceptor returns a grpc client stream interceptor that adds the instanceID on outgoing metadata.
// instanceID is used for multiplexing connection if the component supports it.
func instanceIDStreamInterceptor(instanceID string) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		return streamer(metadata.AppendToOutgoingContext(ctx, metadataInstanceID, instanceID), desc, cc, method, opts...)
	}
}

// socketDialer creates a dialer for the given socket.
func socketDialer(socket string, additionalOpts ...grpc.DialOption) GRPCConnectionDialer {
	return func(ctx context.Context, name string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
		additionalOpts = append(additionalOpts, grpc.WithStreamInterceptor(instanceIDStreamInterceptor(name)), grpc.WithUnaryInterceptor(instanceIDUnaryInterceptor(name)))
		return SocketDial(ctx, socket, append(additionalOpts, opts...)...)
	}
}

// SocketDial creates a grpc connection using the given socket.
func SocketDial(ctx context.Context, socket string, additionalOpts ...grpc.DialOption) (*grpc.ClientConn, error) {
	udsSocket := "unix://" + socket
	log.Debugf("using socket defined at '%s'", udsSocket)
	additionalOpts = append(additionalOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))

	grpcConn, err := grpc.DialContext(ctx, udsSocket, additionalOpts...)
	if err != nil {
		return nil, fmt.Errorf("unable to open GRPC connection using socket '%s': %w", udsSocket, err)
	}
	return grpcConn, nil
}

// Dial opens a grpcConnection and creates a new client instance.
func (g *GRPCConnector[TClient]) Dial(name string) error {
	grpcConn, err := g.dialer(g.Context, name)
	if err != nil {
		return fmt.Errorf("unable to open GRPC connection using the dialer: %w", err)
	}
	g.conn = grpcConn

	g.Client = g.clientFactory(grpcConn)

	return nil
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

// NewGRPCConnector creates a new grpc connector for the given client factory and socket file, using the default socket dialer.
func NewGRPCConnector[TClient GRPCClient](socket string, factory func(grpc.ClientConnInterface) TClient) *GRPCConnector[TClient] {
	return NewGRPCConnectorWithDialer(socketDialer(socket), factory)
}
