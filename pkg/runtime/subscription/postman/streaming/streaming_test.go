/*
Copyright 2025 The Dapr Authors
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

package streaming

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	rtv1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/subscription/postman"
)

// mockAdapterStreamer is a test stub for rtpubsub.AdapterStreamer.
type mockAdapterStreamer struct {
	publishFn func(ctx context.Context, msg *rtpubsub.SubscribedMessage) (*rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1, error)
}

func (m *mockAdapterStreamer) Subscribe(_ rtv1pb.Dapr_SubscribeTopicEventsAlpha1Server, _ *rtv1pb.SubscribeTopicEventsRequestInitialAlpha1, _ rtpubsub.ConnectionID) error {
	return nil
}

func (m *mockAdapterStreamer) Publish(ctx context.Context, msg *rtpubsub.SubscribedMessage) (*rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1, error) {
	return m.publishFn(ctx, msg)
}

func (m *mockAdapterStreamer) StreamerKey(pubsubName, topic string) string {
	return "___" + pubsubName + "||" + topic
}

func (m *mockAdapterStreamer) Close(_ string, _ rtpubsub.ConnectionID) {}

func testMessage(eventID string) *rtpubsub.SubscribedMessage {
	return &rtpubsub.SubscribedMessage{
		CloudEvent: map[string]any{
			contribpubsub.IDField: eventID,
		},
		Topic:    "topic1",
		Data:     []byte("test data"),
		Metadata: map[string]string{"pubsubName": "testpubsub"},
		Path:     "topic1",
		PubSub:   "testpubsub",
	}
}

func makeProcessed(status rtv1pb.TopicEventResponse_TopicEventResponseStatus) *rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1 {
	return &rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1{
		Status: &rtv1pb.TopicEventResponse{Status: status},
	}
}

// TestDeliver validates that Deliver maps every TopicEventResponse status code
// and publish errors to the correct return value.
func TestDeliver(t *testing.T) {
	t.Parallel()

	msg := testMessage("test-event-id")

	testcases := []struct {
		name        string
		publishResp *rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1
		publishErr  error
		wantErr     error
	}{
		{
			name:        "SUCCESS status returns nil",
			publishResp: makeProcessed(rtv1pb.TopicEventResponse_SUCCESS), //nolint:nosnakecase
		},
		{
			name:        "uninitialized status (0) is treated as SUCCESS",
			publishResp: makeProcessed(rtv1pb.TopicEventResponse_TopicEventResponseStatus(0)),
		},
		{
			name:        "RETRY status returns retriable error",
			publishResp: makeProcessed(rtv1pb.TopicEventResponse_RETRY), //nolint:nosnakecase
			wantErr: fmt.Errorf(
				"RETRY status returned from app while processing pub/sub event %v: %w",
				"test-event-id",
				rterrors.NewRetriable(nil),
			),
		},
		{
			name:        "DROP status returns ErrMessageDropped",
			publishResp: makeProcessed(rtv1pb.TopicEventResponse_DROP), //nolint:nosnakecase
			wantErr:     rtpubsub.ErrMessageDropped,
		},
		{
			name:        "unknown status returns retriable error",
			publishResp: makeProcessed(rtv1pb.TopicEventResponse_TopicEventResponseStatus(999)),
			wantErr: fmt.Errorf(
				"unknown status returned from app while processing pub/sub event %v, status: %v, err: %w",
				"test-event-id",
				makeProcessed(rtv1pb.TopicEventResponse_TopicEventResponseStatus(999)).GetStatus(),
				rterrors.NewRetriable(nil),
			),
		},
		{
			name:       "publish error is propagated",
			publishErr: errors.New("channel closed"),
			wantErr:    errors.New("channel closed"),
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mock := &mockAdapterStreamer{
				publishFn: func(_ context.Context, _ *rtpubsub.SubscribedMessage) (*rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1, error) {
					return tc.publishResp, tc.publishErr
				},
			}

			s := New(Options{Channel: mock})
			err := s.Deliver(t.Context(), msg)

			if tc.wantErr == nil {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			assert.Equal(t, tc.wantErr.Error(), err.Error())
		})
	}
}

// TestDeliverBulk verifies that bulk delivery is not yet implemented for the
// streaming postman.
func TestDeliverBulk(t *testing.T) {
	t.Parallel()

	s := New(Options{
		Channel: &mockAdapterStreamer{},
	})

	err := s.DeliverBulk(t.Context(), &postman.DeliverBulkRequest{})
	require.Error(t, err)
	assert.Equal(t, "not implemented", err.Error())
}
