/*
Copyright 2024 The Dapr Authors
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

package streamer

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/pkg/config"
	rtv1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
)

const (
	testPubsubName = "testpubsub"
	testTopic      = "testtopic"
)

type mockStream struct {
	grpc.ServerStream
	ctx                  context.Context
	recvChannel          chan *rtv1pb.SubscribeTopicEventsRequestAlpha1
	lastSent             *rtv1pb.SubscribeTopicEventsResponseAlpha1
	subcriptionActivated chan struct{}
}

func (m *mockStream) Context() context.Context {
	return m.ctx
}

func (m *mockStream) Send(resp *rtv1pb.SubscribeTopicEventsResponseAlpha1) error {
	m.lastSent = resp
	id := resp.GetEventMessage().GetId()
	m.recvChannel <- &rtv1pb.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1pb.SubscribeTopicEventsRequestAlpha1_EventProcessed{
			EventProcessed: &rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1{
				Id: id,
			},
		},
	}
	return nil
}

func (m *mockStream) Recv() (*rtv1pb.SubscribeTopicEventsRequestAlpha1, error) {
	m.subcriptionActivated <- struct{}{} // Indicate to the test that the subscription is activated
	return <-m.recvChannel, nil
}

func TestPublish(t *testing.T) {
	t.Run("publish to subscription should include metadata", func(t *testing.T) {
		s := New(t.Context(), Options{
			TracingSpec: &config.TracingSpec{},
		})

		stream := &mockStream{
			ctx:                  t.Context(),
			recvChannel:          make(chan *rtv1pb.SubscribeTopicEventsRequestAlpha1),
			subcriptionActivated: make(chan struct{}),
		}

		// Start subscription in a goroutine since it blocks
		go func() {
			_ = s.Subscribe(stream, &rtv1pb.SubscribeTopicEventsRequestInitialAlpha1{
				PubsubName: testPubsubName,
				Topic:      testTopic,
			}, 1)
		}()

		// Wait for the subscription to be activated
		<-stream.subcriptionActivated

		// Now publish
		publishMsg := &rtpubsub.SubscribedMessage{
			PubSub:       testPubsubName,
			Topic:        testTopic,
			SubscriberID: 1,
			CloudEvent: map[string]interface{}{
				contribpubsub.IDField:              "123",
				contribpubsub.DataBase64Field:      base64.StdEncoding.EncodeToString([]byte("test")),
				contribpubsub.DataContentTypeField: "text/plain",
			},
			Metadata: map[string]string{
				"md_key1": "md_value1",
				"md_key2": "md_value2",
			},
		}

		_, err := s.Publish(t.Context(), publishMsg)
		require.NoError(t, err)
		msg := stream.lastSent.GetEventMessage()
		assert.NotNil(t, msg)
		data := msg.GetData()
		assert.Equal(t, "test", string(data))
		extensions := msg.GetExtensions()
		assert.NotNil(t, extensions)
		extensionsMap := extensions.AsMap()
		assert.Equal(t, "md_value1", extensionsMap["_metadata_md_key1"])
		assert.Equal(t, "md_value2", extensionsMap["_metadata_md_key2"])
	})
}
