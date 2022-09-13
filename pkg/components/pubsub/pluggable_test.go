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

package pubsub

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"sync"
	"testing"

	guuid "github.com/google/uuid"

	contribMetadata "github.com/dapr/components-contrib/metadata"
	"github.com/dapr/components-contrib/pubsub"

	"go.uber.org/atomic"

	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/dapr/pkg/components/pluggable"
	proto "github.com/dapr/dapr/pkg/proto/components/v1"

	"github.com/dapr/kit/logger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/grpc"
)

var testLogger = logger.NewLogger("pubsub-pluggable-test")

type server struct {
	proto.UnimplementedPubSubServer
	initCalled        atomic.Int64
	onInitCalled      func(*proto.PubSubInitRequest)
	initErr           error
	featuresCalled    atomic.Int64
	featuresErr       error
	publishCalled     atomic.Int64
	onPublishCalled   func(*proto.PublishRequest)
	publishErr        error
	subscribeCalled   atomic.Int64
	onSubscribeCalled func(*proto.SubscribeRequest)
	subscribeErr      error
	subscribeChan     chan *proto.Message
	pingCalled        atomic.Int64
	pingErr           error
}

func (s *server) Init(_ context.Context, req *proto.PubSubInitRequest) (*proto.PubSubInitResponse, error) {
	s.initCalled.Add(1)
	if s.onInitCalled != nil {
		s.onInitCalled(req)
	}
	return &proto.PubSubInitResponse{}, s.initErr
}

func (s *server) Features(context.Context, *proto.FeaturesRequest) (*proto.FeaturesResponse, error) {
	s.featuresCalled.Add(1)
	return &proto.FeaturesResponse{}, s.featuresErr
}

func (s *server) Publish(_ context.Context, req *proto.PublishRequest) (*proto.PublishResponse, error) {
	s.publishCalled.Add(1)
	if s.onPublishCalled != nil {
		s.onPublishCalled(req)
	}
	return &proto.PublishResponse{}, s.publishErr
}

func (s *server) Ping(context.Context, *proto.PingRequest) (*proto.PingResponse, error) {
	s.pingCalled.Add(1)
	return &proto.PingResponse{}, s.pingErr
}

//nolint:nosnakecase
func (s *server) Subscribe(req *proto.SubscribeRequest, stream proto.PubSub_SubscribeServer) error {
	s.subscribeCalled.Add(1)
	if s.onSubscribeCalled != nil {
		s.onSubscribeCalled(req)
	}
	if s.subscribeChan != nil {
		for msg := range s.subscribeChan {
			stream.Send(msg)
		}
	}
	return s.subscribeErr
}

func TestPubSubPluggableCalls(t *testing.T) {
	getPubSub := components.PluggableComponentTestServerFor(testLogger, func(s *grpc.Server, svc *server) {
		proto.RegisterPubSubServer(s, svc)
	}, func(cci grpc.ClientConnInterface) *grpcPubSub {
		client := proto.NewPubSubClient(cci)
		pubsub := fromConnector(testLogger, pluggable.NewGRPCConnector(components.Pluggable{}, proto.NewPubSubClient))
		pubsub.Client = client
		return pubsub
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

			connector := pluggable.NewGRPCConnectorWithFactory(func(string) string {
				return socket
			}, proto.NewPubSubClient)
			defer connector.Close()

			listener, err := net.Listen("unix", socket)
			require.NoError(t, err)
			defer listener.Close()
			s := grpc.NewServer()
			srv := &server{}
			proto.RegisterPubSubServer(s, srv)
			go func() {
				if serveErr := s.Serve(listener); serveErr != nil {
					testLogger.Debugf("Server exited with error: %v", serveErr)
				}
			}()

			ps := fromConnector(testLogger, connector)
			err = ps.Init(pubsub.Metadata{
				Base: contribMetadata.Base{},
			})

			require.NoError(t, err)
			assert.Equal(t, int64(1), srv.featuresCalled.Load())
			assert.Equal(t, int64(1), srv.initCalled.Load())
		})
	}

	t.Run("features should return the component features'", func(t *testing.T) {
		ps, cleanup, err := getPubSub(&server{})
		require.NoError(t, err)
		defer cleanup()
		assert.Empty(t, ps.Features())
		ps.features = []pubsub.Feature{pubsub.FeatureMessageTTL}
		assert.NotEmpty(t, ps.Features())
		assert.Equal(t, ps.Features()[0], pubsub.FeatureMessageTTL)
	})

	t.Run("publish should call publish grpc method", func(t *testing.T) {
		const fakeTopic = "fakeTopic"

		svc := &server{
			onPublishCalled: func(req *proto.PublishRequest) {
				assert.Equal(t, req.Topic, fakeTopic)
			},
		}
		ps, cleanup, err := getPubSub(svc)
		require.NoError(t, err)
		defer cleanup()

		err = ps.Publish(&pubsub.PublishRequest{
			Topic: fakeTopic,
		})

		require.NoError(t, err)
		assert.Equal(t, int64(1), svc.publishCalled.Load())
	})

	t.Run("publish should return an error if grpc method returns an error", func(t *testing.T) {
		const fakeTopic = "fakeTopic"

		svc := &server{
			onPublishCalled: func(req *proto.PublishRequest) {
				assert.Equal(t, req.Topic, fakeTopic)
			},
			publishErr: errors.New("fake-publish-err"),
		}
		ps, cleanup, err := getPubSub(svc)
		require.NoError(t, err)
		defer cleanup()

		err = ps.Publish(&pubsub.PublishRequest{
			Topic: fakeTopic,
		})

		assert.NotNil(t, err)
		assert.Equal(t, int64(1), svc.publishCalled.Load())
	})

	t.Run("subscribe should call subscribe grpc method", func(t *testing.T) {
		const fakeTopic = "fakeTopic"
		var subscribeCalledWg sync.WaitGroup
		subscribeCalledWg.Add(1)

		svc := &server{
			onSubscribeCalled: func(req *proto.SubscribeRequest) {
				subscribeCalledWg.Done()
				assert.Equal(t, req.Topic, fakeTopic)
			},
		}
		ps, cleanup, err := getPubSub(svc)
		require.NoError(t, err)
		defer cleanup()

		err = ps.Subscribe(context.Background(), pubsub.SubscribeRequest{
			Topic: fakeTopic,
		}, func(context.Context, *pubsub.NewMessage) error {
			assert.Fail(t, "handler should not be called")
			return nil
		})

		subscribeCalledWg.Wait()

		require.NoError(t, err)
		assert.Equal(t, int64(1), svc.subscribeCalled.Load())
	})

	t.Run("subscribe should callback handler when new messages arrive", func(t *testing.T) {
		const fakeTopic, fakeData1, fakeData2 = "fakeTopic", "fakeData1", "fakeData2"
		var subscribeCalledWg sync.WaitGroup
		subscribeCalledWg.Add(1)

		messagesData := [][]byte{[]byte(fakeData1), []byte(fakeData2)}
		messages := make([]*proto.Message, len(messagesData))

		for idx, data := range messagesData {
			messages[idx] = &proto.Message{
				Data:        data,
				Topic:       fakeTopic,
				Metadata:    map[string]string{},
				ContentType: "",
			}
		}

		messageChan := make(chan *proto.Message, len(messages))
		defer close(messageChan)

		for _, message := range messages {
			messageChan <- message
		}

		svc := &server{
			onSubscribeCalled: func(req *proto.SubscribeRequest) {
				subscribeCalledWg.Done()
				assert.Equal(t, req.Topic, fakeTopic)
			},
			subscribeChan: messageChan,
		}
		ps, cleanup, err := getPubSub(svc)
		require.NoError(t, err)
		defer cleanup()

		var messagesWg sync.WaitGroup

		messagesWg.Add(len(messages))
		called := atomic.Int64{}

		err = ps.Subscribe(context.Background(), pubsub.SubscribeRequest{
			Topic: fakeTopic,
		}, func(_ context.Context, m *pubsub.NewMessage) error {
			called.Add(1)
			messagesWg.Done()
			assert.Contains(t, messagesData, m.Data)
			return nil
		})

		subscribeCalledWg.Wait()
		messagesWg.Wait()

		require.NoError(t, err)
		assert.Equal(t, int64(1), svc.subscribeCalled.Load())
		assert.Equal(t, int64(len(messages)), called.Load())
	})
}
