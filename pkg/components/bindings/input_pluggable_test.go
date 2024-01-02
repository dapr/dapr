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
	"sync"
	"sync/atomic"
	"testing"

	guuid "github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/dapr/components-contrib/bindings"
	contribMetadata "github.com/dapr/components-contrib/metadata"
	"github.com/dapr/dapr/pkg/components/pluggable"
	proto "github.com/dapr/dapr/pkg/proto/components/v1"
	testingGrpc "github.com/dapr/dapr/pkg/testing/grpc"
	"github.com/dapr/kit/logger"
)

type inputBindingServer struct {
	proto.UnimplementedInputBindingServer
	initCalled            atomic.Int64
	initErr               error
	onInitCalled          func(*proto.InputBindingInitRequest)
	readCalled            atomic.Int64
	readResponseChan      chan *proto.ReadResponse
	readErr               error
	onReadRequestReceived func(*proto.ReadRequest)
}

func (b *inputBindingServer) Init(_ context.Context, req *proto.InputBindingInitRequest) (*proto.InputBindingInitResponse, error) {
	b.initCalled.Add(1)
	if b.onInitCalled != nil {
		b.onInitCalled(req)
	}
	return &proto.InputBindingInitResponse{}, b.initErr
}

//nolint:nosnakecase
func (b *inputBindingServer) Read(stream proto.InputBinding_ReadServer) error {
	b.readCalled.Add(1)
	if b.onReadRequestReceived != nil {
		go func() {
			for {
				msg, err := stream.Recv()
				if err != nil {
					return
				}
				b.onReadRequestReceived(msg)
			}
		}()
	}
	if b.readResponseChan != nil {
		for resp := range b.readResponseChan {
			if err := stream.Send(resp); err != nil {
				return err
			}
		}
	}
	return b.readErr
}

func (b *inputBindingServer) Ping(context.Context, *proto.PingRequest) (*proto.PingResponse, error) {
	return &proto.PingResponse{}, nil
}

var testLogger = logger.NewLogger("bindings-test-pluggable-logger")

func TestInputBindingCalls(t *testing.T) {
	getInputBinding := testingGrpc.TestServerFor(testLogger, func(s *grpc.Server, svc *inputBindingServer) {
		proto.RegisterInputBindingServer(s, svc)
	}, func(cci grpc.ClientConnInterface) *grpcInputBinding {
		client := proto.NewInputBindingClient(cci)
		inbinding := inputFromConnector(testLogger, pluggable.NewGRPCConnector("/tmp/socket.sock", proto.NewInputBindingClient))
		inbinding.Client = client
		return inbinding
	})
	if runtime.GOOS != "windows" {
		t.Run("test init should populate features and call grpc init", func(t *testing.T) {
			const (
				fakeName          = "name"
				fakeType          = "type"
				fakeVersion       = "v1"
				fakeComponentName = "component"
				fakeSocketFolder  = "/tmp"
			)

			uniqueID := guuid.New().String()
			socket := fmt.Sprintf("%s/%s.sock", fakeSocketFolder, uniqueID)
			defer os.Remove(socket)

			connector := pluggable.NewGRPCConnector(socket, proto.NewInputBindingClient)
			defer connector.Close()

			listener, err := net.Listen("unix", socket)
			require.NoError(t, err)
			defer listener.Close()
			s := grpc.NewServer()
			srv := &inputBindingServer{}
			proto.RegisterInputBindingServer(s, srv)
			go func() {
				if serveErr := s.Serve(listener); serveErr != nil {
					testLogger.Debugf("Server exited with error: %v", serveErr)
				}
			}()

			conn := inputFromConnector(testLogger, connector)
			err = conn.Init(context.Background(), bindings.Metadata{
				Base: contribMetadata.Base{},
			})

			require.NoError(t, err)
			assert.Equal(t, int64(1), srv.initCalled.Load())
		})
	}

	t.Run("read should callback handler when new messages are available", func(t *testing.T) {
		const fakeData1, fakeData2 = "fakeData1", "fakeData2"
		messagesData := [][]byte{[]byte(fakeData1), []byte(fakeData2)}
		var (
			messagesAcked       sync.WaitGroup
			messagesProcessed   sync.WaitGroup
			totalResponseErrors atomic.Int64
			handleCalled        atomic.Int64
		)

		messages := make([]*proto.ReadResponse, len(messagesData))

		for idx, data := range messagesData {
			messages[idx] = &proto.ReadResponse{
				Data: data,
			}
		}

		messagesAcked.Add(len(messages))
		messagesProcessed.Add(len(messages))

		messageChan := make(chan *proto.ReadResponse, len(messages))
		defer close(messageChan)

		for _, message := range messages {
			messageChan <- message
		}

		srv := &inputBindingServer{
			readResponseChan: messageChan,
			onReadRequestReceived: func(ma *proto.ReadRequest) {
				messagesAcked.Done()
				if ma.GetResponseError() != nil {
					totalResponseErrors.Add(1)
				}
			},
		}

		handleErrors := make(chan error, 1) // simulating an ack error
		handleErrors <- errors.New("fake-error")
		close(handleErrors)

		binding, cleanup, err := getInputBinding(srv)
		require.NoError(t, err)
		defer cleanup()

		err = binding.Read(context.Background(), func(_ context.Context, resp *bindings.ReadResponse) ([]byte, error) {
			handleCalled.Add(1)
			messagesProcessed.Done()
			assert.Contains(t, messagesData, resp.Data)
			return []byte{}, <-handleErrors
		})
		require.NoError(t, err)

		messagesProcessed.Wait()
		messagesAcked.Wait()

		assert.Equal(t, int64(len(messages)), handleCalled.Load())
		assert.Equal(t, int64(1), totalResponseErrors.Load()) // at least one message should be an error
	})
}
