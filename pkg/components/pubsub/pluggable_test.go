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
	"sync/atomic"
	"testing"

	guuid "github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	contribMetadata "github.com/dapr/components-contrib/metadata"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/pkg/components/pluggable"
	proto "github.com/dapr/dapr/pkg/proto/components/v1"
	testingGrpc "github.com/dapr/dapr/pkg/testing/grpc"
	"github.com/dapr/kit/logger"
)

var testLogger = logger.NewLogger("pubsub-pluggable-test")

type server struct {
	proto.UnimplementedPubSubServer
	initCalled      atomic.Int64
	onInitCalled    func(*proto.PubSubInitRequest)
	initErr         error
	featuresCalled  atomic.Int64
	featuresErr     error
	publishCalled   atomic.Int64
	onPublishCalled func(*proto.PublishRequest)
	publishErr      error
	pullChan        chan *proto.PullMessagesResponse
	pingCalled      atomic.Int64
	pingErr         error
	onAckReceived   func(*proto.PullMessagesRequest)
	pullCalled      atomic.Int64
	pullErr         error
}

//nolint:nosnakecase
func (s *server) PullMessages(svc proto.PubSub_PullMessagesServer) error {
	s.pullCalled.Add(1)

	if s.onAckReceived != nil {
		go func() {
			for {
				msg, err := svc.Recv()
				if err != nil {
					return
				}
				s.onAckReceived(msg)
			}
		}()
	}
	if s.pullChan != nil {
		for msg := range s.pullChan {
			if err := svc.Send(msg); err != nil {
				return err
			}
		}
	}

	return s.pullErr
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

func TestPubSubPluggableCalls(t *testing.T) {
	getPubSub := testingGrpc.TestServerFor(testLogger, func(s *grpc.Server, svc *server) {
		proto.RegisterPubSubServer(s, svc)
	}, func(cci grpc.ClientConnInterface) *grpcPubSub {
		client := proto.NewPubSubClient(cci)
		pubsub := fromConnector(testLogger, pluggable.NewGRPCConnector("/tmp/socket.sock", proto.NewPubSubClient))
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

			connector := pluggable.NewGRPCConnector(socket, proto.NewPubSubClient)
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
			err = ps.Init(context.Background(), pubsub.Metadata{
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
		assert.Equal(t, pubsub.FeatureMessageTTL, ps.Features()[0])
	})

	t.Run("publish should call publish grpc method", func(t *testing.T) {
		const fakeTopic = "fakeTopic"

		svc := &server{
			onPublishCalled: func(req *proto.PublishRequest) {
				assert.Equal(t, fakeTopic, req.GetTopic())
			},
		}
		ps, cleanup, err := getPubSub(svc)
		require.NoError(t, err)
		defer cleanup()

		err = ps.Publish(context.Background(), &pubsub.PublishRequest{
			Topic: fakeTopic,
		})

		require.NoError(t, err)
		assert.Equal(t, int64(1), svc.publishCalled.Load())
	})

	t.Run("publish should return an error if grpc method returns an error", func(t *testing.T) {
		const fakeTopic = "fakeTopic"

		svc := &server{
			onPublishCalled: func(req *proto.PublishRequest) {
				assert.Equal(t, fakeTopic, req.GetTopic())
			},
			publishErr: errors.New("fake-publish-err"),
		}
		ps, cleanup, err := getPubSub(svc)
		require.NoError(t, err)
		defer cleanup()

		err = ps.Publish(context.Background(), &pubsub.PublishRequest{
			Topic: fakeTopic,
		})

		require.Error(t, err)
		assert.Equal(t, int64(1), svc.publishCalled.Load())
	})

	t.Run("subscribe should callback handler when new messages arrive", func(t *testing.T) {
		const fakeTopic, fakeData1, fakeData2 = "fakeTopic", "fakeData1", "fakeData2"
		var (
			messagesAcked     sync.WaitGroup
			topicSent         sync.WaitGroup
			messagesProcessed sync.WaitGroup
			totalAckErrors    atomic.Int64
			handleCalled      atomic.Int64
		)

		messagesData := [][]byte{[]byte(fakeData1), []byte(fakeData2)}
		messages := make([]*proto.PullMessagesResponse, len(messagesData))

		messagesAcked.Add(len(messages))
		messagesProcessed.Add(len(messages))
		topicSent.Add(1)

		for idx, data := range messagesData {
			messages[idx] = &proto.PullMessagesResponse{
				Data:        data,
				TopicName:   fakeTopic,
				Metadata:    map[string]string{},
				ContentType: "",
			}
		}

		messageChan := make(chan *proto.PullMessagesResponse, len(messages))
		defer close(messageChan)

		for _, message := range messages {
			messageChan <- message
		}

		svc := &server{
			pullChan: messageChan,
			onAckReceived: func(ma *proto.PullMessagesRequest) {
				if ma.GetTopic() != nil {
					topicSent.Done()
				} else {
					messagesAcked.Done()
				}
				if ma.GetAckError() != nil {
					totalAckErrors.Add(1)
				}
			},
		}

		ps, cleanup, err := getPubSub(svc)
		require.NoError(t, err)
		defer cleanup()

		handleErrors := make(chan error, 1) // simulating an ack error
		handleErrors <- errors.New("fake-error")
		close(handleErrors)

		err = ps.Subscribe(context.Background(), pubsub.SubscribeRequest{
			Topic: fakeTopic,
		}, func(_ context.Context, m *pubsub.NewMessage) error {
			handleCalled.Add(1)
			messagesProcessed.Done()
			assert.Contains(t, messagesData, m.Data)
			return <-handleErrors
		})
		require.NoError(t, err)

		topicSent.Wait()
		messagesProcessed.Wait()
		messagesAcked.Wait()

		assert.Equal(t, int64(len(messages)), handleCalled.Load())
		assert.Equal(t, int64(1), totalAckErrors.Load()) // at least one message should be an error
	})
}
