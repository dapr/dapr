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

package bindings

import (
	"context"
	"net"
	"testing"

	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/dapr/pkg/components/pluggable"
	proto "github.com/dapr/dapr/pkg/proto/components/v1"

	"go.uber.org/atomic"

	"github.com/dapr/kit/logger"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1024 * 1024

type inputBindingServer struct {
	proto.UnimplementedInputBindingServer
	initCalled   atomic.Int64
	initErr      error
	onInitCalled func(*proto.InputBindingInitRequest)
}

func (b *inputBindingServer) Init(_ context.Context, req *proto.InputBindingInitRequest) (*proto.InputBindingInitResponse, error) {
	b.initCalled.Add(1)
	if b.onInitCalled != nil {
		b.onInitCalled(req)
	}
	return &proto.InputBindingInitResponse{}, b.initErr
}

var testLogger = logger.NewLogger("bindings-test-pluggable-logger")

// getInputBinding returns a inputbinding connected to the given server
func getInputBinding(srv *inputBindingServer) (inputBinding *grpcInputBinding, cleanup func(), err error) {
	lis := bufconn.Listen(bufSize)
	s := grpc.NewServer()
	proto.RegisterInputBindingServer(s, srv)
	go func() {
		if serveErr := s.Serve(lis); serveErr != nil {
			testLogger.Debugf("Server exited with error: %v", serveErr)
		}
	}()
	ctx := context.Background()
	conn, err := grpc.DialContext(ctx, "bufnet", grpc.WithContextDialer(func(ctx context.Context, s string) (net.Conn, error) {
		return lis.Dial()
	}), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}

	client := proto.NewInputBindingClient(conn)
	inputBinding = inputFromConnector(testLogger, pluggable.NewGRPCConnector(components.Pluggable{}, proto.NewInputBindingClient))
	inputBinding.Client = client
	return inputBinding, func() {
		lis.Close()
		conn.Close()
	}, nil
}

func TestInputBindingCalls(t *testing.T) {}
