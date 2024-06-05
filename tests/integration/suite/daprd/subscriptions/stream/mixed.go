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
	"google.golang.org/protobuf/types/known/emptypb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/subscriber"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(mixed))
}

type mixed struct {
	daprd *daprd.Daprd
	sub   *subscriber.Subscriber
}

func (m *mixed) Setup(t *testing.T) []framework.Option {
	m.sub = subscriber.New(t,
		subscriber.WithListTopicSubscriptions(func(context.Context, *emptypb.Empty) (*rtv1.ListTopicSubscriptionsResponse, error) {
			return &rtv1.ListTopicSubscriptionsResponse{
				Subscriptions: []*rtv1.TopicSubscription{{
					PubsubName: "mypub",
					Topic:      "a",
					Routes:     &rtv1.TopicRoutes{Default: "/123"},
				}},
			}, nil
		}),
	)

	m.daprd = daprd.New(t,
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(m.sub.Port(t)),
		daprd.WithResourceFiles(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypub
spec:
  type: pubsub.in-memory
  version: v1
---
apiVersion: dapr.io/v1alpha1
Kind: Subscription
metadata:
 name: sub
spec:
 pubsubname: mypub
 topic: b
 route: /zyx
`))

	return []framework.Option{
		framework.WithProcesses(m.sub, m.daprd),
	}
}

func (m *mixed) Run(t *testing.T, ctx context.Context) {
	m.daprd.WaitUntilRunning(t, ctx)

	client := m.daprd.GRPCClient(t, ctx)

	stream, err := client.SubscribeTopicEventsAlpha1(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_InitialRequest{
			InitialRequest: &rtv1.SubscribeTopicEventsInitialRequestAlpha1{
				PubsubName: "mypub", Topic: "c",
			},
		},
	}))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, m.daprd.GetMetaSubscriptions(c, ctx), 3)
	}, time.Second*10, time.Millisecond*10)

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub", Topic: "a", Data: []byte(`{"status": "completed"}`),
	})
	require.NoError(t, err)
	assert.Equal(t, "/123", m.sub.Receive(t, ctx).GetPath())

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub", Topic: "b", Data: []byte(`{"status": "completed"}`),
	})
	require.NoError(t, err)
	assert.Equal(t, "/zyx", m.sub.Receive(t, ctx).GetPath())

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub", Topic: "c", Data: []byte(`{"status": "completed"}`),
	})
	require.NoError(t, err)
	event, err := stream.Recv()
	require.NoError(t, err)
	assert.Equal(t, "c", event.GetTopic())
	require.NoError(t, stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_EventResponse{
			EventResponse: &rtv1.SubscribeTopicEventsResponseAlpha1{
				Id:     event.GetId(),
				Status: &rtv1.TopicEventResponse{Status: rtv1.TopicEventResponse_SUCCESS},
			},
		},
	}))

	stream, err = client.SubscribeTopicEventsAlpha1(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_InitialRequest{
			InitialRequest: &rtv1.SubscribeTopicEventsInitialRequestAlpha1{
				PubsubName: "mypub", Topic: "a",
			},
		},
	}))
	// TODO: @joshvanl: expose bi-direction subscriptions to measure so we don't
	// need to sleep.
	time.Sleep(time.Second)
	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub", Topic: "a", Data: []byte(`{"status": "completed"}`),
	})
	require.NoError(t, err)
	event, err = stream.Recv()
	require.NoError(t, err)
	require.NoError(t, stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_EventResponse{
			EventResponse: &rtv1.SubscribeTopicEventsResponseAlpha1{
				Id:     event.GetId(),
				Status: &rtv1.TopicEventResponse{Status: rtv1.TopicEventResponse_SUCCESS},
			},
		},
	}))

	require.NoError(t, stream.CloseSend())
	// TODO: @joshvanl: expose bi-direction subscriptions to measure so we don't
	// need to sleep.
	time.Sleep(time.Second)
	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub", Topic: "a", Data: []byte(`{"status": "completed"}`),
	})
	require.NoError(t, err)
	assert.Equal(t, "a", m.sub.Receive(t, ctx).GetTopic())

	stream, err = client.SubscribeTopicEventsAlpha1(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_InitialRequest{
			InitialRequest: &rtv1.SubscribeTopicEventsInitialRequestAlpha1{
				PubsubName: "mypub", Topic: "a",
			},
		},
	}))
	// TODO: @joshvanl: expose bi-direction subscriptions to measure so we don't
	// need to sleep.
	time.Sleep(time.Second)
	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub", Topic: "a", Data: []byte(`{"status": "completed"}`),
	})
	require.NoError(t, err)
	event, err = stream.Recv()
	require.NoError(t, err)
	require.NoError(t, stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_EventResponse{
			EventResponse: &rtv1.SubscribeTopicEventsResponseAlpha1{
				Id:     event.GetId(),
				Status: &rtv1.TopicEventResponse{Status: rtv1.TopicEventResponse_SUCCESS},
			},
		},
	}))
}
