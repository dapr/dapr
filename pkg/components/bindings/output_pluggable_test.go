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
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"sync/atomic"
	"testing"

	guuid "github.com/google/uuid"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/dapr/pkg/components/pluggable"
	proto "github.com/dapr/dapr/pkg/proto/components/v1"
	testingGrpc "github.com/dapr/dapr/pkg/testing/grpc"

	contribMetadata "github.com/dapr/components-contrib/metadata"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/grpc"
)

type outputBindingServer struct {
	proto.UnimplementedOutputBindingServer
	initCalled           atomic.Int64
	initErr              error
	onInitCalled         func(*proto.OutputBindingInitRequest)
	invokeCalled         atomic.Int64
	invokeErr            error
	onInvokeCalled       func(*proto.InvokeRequest)
	invokeResp           *proto.InvokeResponse
	listOperationsCalled atomic.Int64
	listOperationsErr    error
	listOperationsResp   *proto.ListOperationsResponse
}

func (b *outputBindingServer) Init(_ context.Context, req *proto.OutputBindingInitRequest) (*proto.OutputBindingInitResponse, error) {
	b.initCalled.Add(1)
	if b.onInitCalled != nil {
		b.onInitCalled(req)
	}
	return &proto.OutputBindingInitResponse{}, b.initErr
}

func (b *outputBindingServer) Invoke(_ context.Context, req *proto.InvokeRequest) (*proto.InvokeResponse, error) {
	b.invokeCalled.Add(1)
	if b.onInvokeCalled != nil {
		b.onInvokeCalled(req)
	}
	return b.invokeResp, b.invokeErr
}

func (b *outputBindingServer) ListOperations(context.Context, *proto.ListOperationsRequest) (*proto.ListOperationsResponse, error) {
	b.listOperationsCalled.Add(1)
	resp := b.listOperationsResp
	if resp == nil {
		resp = &proto.ListOperationsResponse{}
	}
	return resp, b.listOperationsErr
}

func (b *outputBindingServer) Ping(context.Context, *proto.PingRequest) (*proto.PingResponse, error) {
	return &proto.PingResponse{}, nil
}

func TestOutputBindingCalls(t *testing.T) {
	getOutputBinding := testingGrpc.TestServerFor(testLogger, func(s *grpc.Server, svc *outputBindingServer) {
		proto.RegisterOutputBindingServer(s, svc)
	}, func(cci grpc.ClientConnInterface) *grpcOutputBinding {
		client := proto.NewOutputBindingClient(cci)
		outbinding := outputFromConnector(testLogger, pluggable.NewGRPCConnector("/tmp/socket.sock", proto.NewOutputBindingClient))
		outbinding.Client = client
		return outbinding
	})
	if runtime.GOOS != "windows" {
		t.Run("test init should populate features and call grpc init", func(t *testing.T) {
			const (
				fakeName          = "name"
				fakeType          = "type"
				fakeVersion       = "v1"
				fakeComponentName = "component"
				fakeSocketFolder  = "/tmp"
				fakeOperation     = "fake"
			)

			fakeOperations := []string{fakeOperation}

			uniqueID := guuid.New().String()
			socket := fmt.Sprintf("%s/%s.sock", fakeSocketFolder, uniqueID)
			defer os.Remove(socket)

			connector := pluggable.NewGRPCConnector(socket, proto.NewOutputBindingClient)
			defer connector.Close()

			listener, err := net.Listen("unix", socket)
			require.NoError(t, err)
			defer listener.Close()
			s := grpc.NewServer()
			srv := &outputBindingServer{
				listOperationsResp: &proto.ListOperationsResponse{
					Operations: fakeOperations,
				},
			}
			proto.RegisterOutputBindingServer(s, srv)
			go func() {
				if serveErr := s.Serve(listener); serveErr != nil {
					testLogger.Debugf("Server exited with error: %v", serveErr)
				}
			}()

			conn := outputFromConnector(testLogger, connector)
			err = conn.Init(context.Background(), bindings.Metadata{
				Base: contribMetadata.Base{},
			})

			require.NoError(t, err)
			assert.Equal(t, int64(1), srv.listOperationsCalled.Load())
			assert.Equal(t, int64(1), srv.initCalled.Load())
			assert.ElementsMatch(t, conn.operations, []bindings.OperationKind{fakeOperation})
		})
	}

	t.Run("list operations should return operations when set", func(t *testing.T) {
		const fakeOperation = "fake"
		ops := []bindings.OperationKind{fakeOperation}
		outputGrpc := &grpcOutputBinding{
			operations: ops,
		}

		assert.Equal(t, ops, outputGrpc.Operations())
	})

	t.Run("invoke should call grpc invoke when called", func(t *testing.T) {
		const fakeOp, fakeMetadataKey, fakeMetadataValue = "fake-op", "fake-key", "fake-value"
		fakeDataResp := []byte("fake-resp")

		fakeDataReq := []byte("fake-req")
		fakeMetadata := map[string]string{
			fakeMetadataKey: fakeMetadataValue,
		}

		srv := &outputBindingServer{
			invokeResp: &proto.InvokeResponse{
				Data:     fakeDataResp,
				Metadata: fakeMetadata,
			},
			onInvokeCalled: func(ir *proto.InvokeRequest) {
				assert.Equal(t, fakeOp, ir.GetOperation())
			},
		}

		outputSvc, cleanup, err := getOutputBinding(srv)
		defer cleanup()
		require.NoError(t, err)

		resp, err := outputSvc.Invoke(context.Background(), &bindings.InvokeRequest{
			Data:      fakeDataReq,
			Metadata:  fakeMetadata,
			Operation: fakeOp,
		})

		require.NoError(t, err)

		assert.Equal(t, int64(1), srv.invokeCalled.Load())
		assert.Equal(t, resp.Data, fakeDataResp)
	})

	t.Run("invoke should return an error if grpc method returns an error", func(t *testing.T) {
		const errStr = "fake-invoke-err"

		srv := &outputBindingServer{
			invokeErr: errors.New(errStr),
		}

		outputSvc, cleanup, err := getOutputBinding(srv)
		defer cleanup()
		require.NoError(t, err)

		_, err = outputSvc.Invoke(context.Background(), &bindings.InvokeRequest{})

		require.Error(t, err)
		assert.Equal(t, int64(1), srv.invokeCalled.Load())
	})
}
