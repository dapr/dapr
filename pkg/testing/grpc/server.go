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

package grpc

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/kit/logger"
)

const (
	bufSize = 1024 * 1024

	MaxGRPCServerUptime = 200 * time.Millisecond
)

// TestServerFor returns a grpcServer factory that bootstraps a grpcserver backed by a buf connection (in memory), and returns the given clientFactory instance to communicate with it.
// it also provides cleanup function for close the grpcserver and client connection.
//
//	usage,
//
//		serverFactory := testingGrpc.TestServerFor(testLogger, func(s *grpc.Server, svc *your_service_goes_here) {
//				proto.RegisterMyService(s, svc) // your service
//		}, proto.NewMyServiceClient)
//
//	 	client, cleanup, err := serverFactory(&your_service{})
//		require.NoError(t, err)
//		defer cleanup()
func TestServerFor[TServer any, TClient any](logger logger.Logger, registersvc func(*grpc.Server, TServer), clientFactory func(grpc.ClientConnInterface) TClient) func(svc TServer) (client TClient, cleanup func(), err error) {
	return func(srv TServer) (client TClient, cleanup func(), err error) {
		lis := bufconn.Listen(bufSize)
		s := grpc.NewServer()
		registersvc(s, srv)
		go func() {
			if serveErr := s.Serve(lis); serveErr != nil {
				logger.Debugf("Server exited with error: %v", serveErr)
			}
		}()
		ctx := context.Background()
		conn, err := grpc.DialContext(ctx, "bufnet", grpc.WithContextDialer(func(ctx context.Context, s string) (net.Conn, error) {
			return lis.Dial()
		}), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			var zero TClient
			return zero, nil, err
		}

		return clientFactory(conn), func() {
			lis.Close()
			conn.Close()
		}, nil
	}
}

// TestServerWithDialer returns a grpcServer factory that bootstraps a grpcserver backed by a buf connection (in memory), and returns a connection dialer to communicate with it.
// it also provides cleanup function for close the grpcserver but the client connection should be handled by the caller.
func TestServerWithDialer[TServer any](logger logger.Logger, registersvc func(*grpc.Server, TServer)) func(svc TServer) (dialer func(ctx context.Context, opts ...grpc.DialOption) (*grpc.ClientConn, error), cleanup func(), err error) {
	return func(srv TServer) (dialer func(ctx context.Context, opts ...grpc.DialOption) (*grpc.ClientConn, error), cleanup func(), err error) {
		lis := bufconn.Listen(bufSize)
		s := grpc.NewServer()
		registersvc(s, srv)
		go func() {
			if serveErr := s.Serve(lis); serveErr != nil {
				logger.Debugf("Server exited with error: %v", serveErr)
			}
		}()

		return func(ctx context.Context, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
				opts = append(opts, grpc.WithContextDialer(func(ctx context.Context, s string) (net.Conn, error) {
					return lis.Dial()
				}), grpc.WithTransportCredentials(insecure.NewCredentials()))
				return grpc.DialContext(ctx, "bufnet", opts...)
			}, func() {
				lis.Close()
			}, nil
	}
}

func StartTestAppCallbackGRPCServer(t *testing.T, port int, mockServer runtimev1pb.AppCallbackServer) *grpc.Server {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	require.NoError(t, err)
	grpcServer := grpc.NewServer()
	go func() {
		runtimev1pb.RegisterAppCallbackServer(grpcServer, mockServer)
		if err := grpcServer.Serve(lis); err != nil {
			panic(err)
		}
	}()
	// wait until server starts
	time.Sleep(MaxGRPCServerUptime)

	return grpcServer
}
