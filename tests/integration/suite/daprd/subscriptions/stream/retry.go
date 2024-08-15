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

package stream

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(retry))
}

type retry struct {
	daprd *daprd.Daprd
}

func (r *retry) Setup(t *testing.T) []framework.Option {
	r.daprd = daprd.New(t,
		daprd.WithResourceFiles(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypub
spec:
  type: pubsub.in-memory
  version: v1
`))

	return []framework.Option{
		framework.WithProcesses(r.daprd),
	}
}

func (r *retry) Run(t *testing.T, ctx context.Context) {
	r.daprd.WaitUntilRunning(t, ctx)

	client := r.daprd.GRPCClient(t, ctx)

	stream, err := client.SubscribeTopicEventsAlpha1(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_InitialRequest{
			InitialRequest: &rtv1.SubscribeTopicEventsRequestInitialAlpha1{
				PubsubName: "mypub", Topic: "a",
			},
		},
	}))

	resp, err := stream.Recv()
	require.NoError(t, err)
	switch resp.GetSubscribeTopicEventsResponseType().(type) {
	case *rtv1.SubscribeTopicEventsResponseAlpha1_InitialResponse:
	default:
		require.Failf(t, "unexpected response", "got (%T) %v", resp.GetSubscribeTopicEventsResponseType(), resp)
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, r.daprd.GetMetaSubscriptions(c, ctx), 1)
	}, time.Second*10, time.Millisecond*10)

	pubReq := &rtv1.PublishEventRequest{
		PubsubName: "mypub", Topic: "a",
		Data:            []byte(`{"status": "completed"}`),
		DataContentType: "application/json",
	}
	_, err = client.PublishEvent(ctx, pubReq)
	require.NoError(t, err)

	event, err := stream.Recv()
	require.NoError(t, err)
	assert.Equal(t, "a", event.GetEventMessage().GetTopic())
	id := event.GetEventMessage().GetId()

	require.NoError(t, stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_EventProcessed{
			EventProcessed: &rtv1.SubscribeTopicEventsRequestProcessedAlpha1{
				Id:     event.GetEventMessage().GetId(),
				Status: &rtv1.TopicEventResponse{Status: rtv1.TopicEventResponse_RETRY},
			},
		},
	}))
	event, err = stream.Recv()
	require.NoError(t, err)
	assert.Equal(t, "a", event.GetEventMessage().GetTopic())
	assert.Equal(t, id, event.GetEventMessage().GetId())

	require.NoError(t, stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_EventProcessed{
			EventProcessed: &rtv1.SubscribeTopicEventsRequestProcessedAlpha1{
				Id:     event.GetEventMessage().GetId(),
				Status: &rtv1.TopicEventResponse{Status: rtv1.TopicEventResponse_RETRY},
			},
		},
	}))
	event, err = stream.Recv()
	require.NoError(t, err)
	assert.Equal(t, "a", event.GetEventMessage().GetTopic())
	assert.Equal(t, id, event.GetEventMessage().GetId())

	require.NoError(t, stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_EventProcessed{
			EventProcessed: &rtv1.SubscribeTopicEventsRequestProcessedAlpha1{
				Id:     event.GetEventMessage().GetId(),
				Status: &rtv1.TopicEventResponse{Status: rtv1.TopicEventResponse_DROP},
			},
		},
	}))
	_, err = client.PublishEvent(ctx, pubReq)
	require.NoError(t, err)
	event, err = stream.Recv()
	require.NoError(t, err)
	assert.Equal(t, "a", event.GetEventMessage().GetTopic())
	assert.NotEqual(t, id, event.GetEventMessage().GetId())
	require.NoError(t, stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_EventProcessed{
			EventProcessed: &rtv1.SubscribeTopicEventsRequestProcessedAlpha1{
				Id:     event.GetEventMessage().GetId(),
				Status: &rtv1.TopicEventResponse{Status: rtv1.TopicEventResponse_SUCCESS},
			},
		},
	}))

	_, err = client.PublishEvent(ctx, pubReq)
	require.NoError(t, err)
	event1, err := stream.Recv()
	require.NoError(t, err)

	require.NoError(t, stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_EventProcessed{
			EventProcessed: &rtv1.SubscribeTopicEventsRequestProcessedAlpha1{
				Id:     event1.GetEventMessage().GetId(),
				Status: &rtv1.TopicEventResponse{Status: rtv1.TopicEventResponse_DROP},
			},
		},
	}))

	_, err = client.PublishEvent(ctx, pubReq)
	require.NoError(t, err)
	event2, err := stream.Recv()
	require.NoError(t, err)

	require.NoError(t, stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_EventProcessed{
			EventProcessed: &rtv1.SubscribeTopicEventsRequestProcessedAlpha1{
				Id:     event2.GetEventMessage().GetId(),
				Status: &rtv1.TopicEventResponse{Status: rtv1.TopicEventResponse_RETRY},
			},
		},
	}))

	event3, err := stream.Recv()
	require.NoError(t, err)
	assert.Equal(t, event2.GetEventMessage().GetId(), event3.GetEventMessage().GetId())

	require.NoError(t, stream.CloseSend())
}
