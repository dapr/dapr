/*
Copyright 2026 The Dapr Authors
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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	rtv1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
)

type fakeStream struct {
	grpc.ServerStream
	ctx  context.Context
	sent chan *rtv1pb.SubscribeTopicEventsResponseAlpha1
	recv chan *rtv1pb.SubscribeTopicEventsRequestAlpha1
	// unblock, when set, holds each Send after its message was taken.
	unblock chan struct{}
}

func (f *fakeStream) Context() context.Context { return f.ctx }

func (f *fakeStream) Send(resp *rtv1pb.SubscribeTopicEventsResponseAlpha1) error {
	f.sent <- resp
	if f.unblock != nil {
		<-f.unblock
	}
	return nil
}

func (f *fakeStream) Recv() (*rtv1pb.SubscribeTopicEventsRequestAlpha1, error) {
	select {
	case req := <-f.recv:
		return req, nil
	case <-f.ctx.Done():
		return nil, f.ctx.Err()
	}
}

func TestSubscribePublishDuringStart(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	s := New(t.Context(), Options{}).(*streamer)
	stream := &fakeStream{
		ctx:  ctx,
		sent: make(chan *rtv1pb.SubscribeTopicEventsResponseAlpha1, 2),
		recv: make(chan *rtv1pb.SubscribeTopicEventsRequestAlpha1),
	}
	req := &rtv1pb.SubscribeTopicEventsRequestInitialAlpha1{PubsubName: "mypub", Topic: "a"}

	type result struct {
		resp *rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1
		err  error
	}
	pubCh := make(chan result, 1)

	// start publishes like a processor subscription delivering a message the
	// moment it starts. The publish must wait for the initial response, so it
	// must still be pending when start returns.
	subErr := make(chan error, 1)
	go func() {
		subErr <- s.Subscribe(stream, req, 1, func() error {
			go func() {
				resp, err := s.Publish(ctx, &rtpubsub.SubscribedMessage{
					CloudEvent:   map[string]any{"id": "msg1"},
					Topic:        "a",
					PubSub:       "mypub",
					SubscriberID: 1,
				})
				pubCh <- result{resp, err}
			}()
			select {
			case res := <-pubCh:
				return fmt.Errorf("publish returned before the initial response: %v", res.err)
			case <-time.After(100 * time.Millisecond):
				return nil
			}
		})
	}()

	select {
	case resp := <-stream.sent:
		require.NotNil(t, resp.GetInitialResponse(), "first response must be the initial response, got %v", resp)
	case err := <-subErr:
		require.Fail(t, "subscribe returned before the initial response", "err: %v", err)
	case <-time.After(5 * time.Second):
		require.Fail(t, "timed out waiting for initial response")
	}

	select {
	case resp := <-stream.sent:
		require.Equal(t, "msg1", resp.GetEventMessage().GetId())
	case res := <-pubCh:
		require.Failf(t, "publish returned before the message was sent", "err: %v", res.err)
	case <-time.After(5 * time.Second):
		require.Fail(t, "timed out waiting for event message")
	}

	stream.recv <- &rtv1pb.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1pb.SubscribeTopicEventsRequestAlpha1_EventProcessed{
			EventProcessed: &rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1{
				Id:     "msg1",
				Status: &rtv1pb.TopicEventResponse{Status: rtv1pb.TopicEventResponse_SUCCESS},
			},
		},
	}

	select {
	case res := <-pubCh:
		require.NoError(t, res.err)
		assert.Equal(t, "msg1", res.resp.GetId())
	case <-time.After(5 * time.Second):
		require.Fail(t, "timed out waiting for publish response")
	}

	cancel()
	select {
	case err := <-subErr:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		require.Fail(t, "timed out waiting for Subscribe to return")
	}
}

func TestPublishAckBeforeClose(t *testing.T) {
	for range 50 {
		s := New(t.Context(), Options{}).(*streamer)
		stream := &fakeStream{
			ctx:     t.Context(),
			sent:    make(chan *rtv1pb.SubscribeTopicEventsResponseAlpha1),
			unblock: make(chan struct{}),
		}
		c := &conn{
			stream:           stream,
			connectionID:     1,
			closeCh:          make(chan struct{}),
			ready:            make(chan struct{}),
			publishResponses: make(PublishResponses),
		}
		close(c.ready)
		s.subscribers[s.StreamerKey("mypub", "a")] = Connections{1: c}

		type result struct {
			resp *rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1
			err  error
		}
		pubCh := make(chan result, 1)
		go func() {
			resp, err := s.Publish(t.Context(), &rtpubsub.SubscribedMessage{
				CloudEvent:   map[string]any{"id": "msg1"},
				Topic:        "a",
				PubSub:       "mypub",
				SubscriberID: 1,
			})
			pubCh <- result{resp, err}
		}()

		// The client acks and the stream closes while the publish is still
		// in Send: both are ready when it starts waiting.
		event := <-stream.sent
		c.notifyPublishResponse(&rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1{Id: event.GetEventMessage().GetId()})
		close(c.closeCh)
		stream.unblock <- struct{}{}

		res := <-pubCh
		require.NoError(t, res.err, "the client acked before the stream closed")
		assert.Equal(t, "msg1", res.resp.GetId())
	}
}
