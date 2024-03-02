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

package routes

import (
	"context"
	"testing"

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
	suite.Register(new(match))
}

type match struct {
	daprd *daprd.Daprd
	sub   *subscriber.Subscriber
}

func (m *match) Setup(t *testing.T) []framework.Option {
	m.sub = subscriber.New(t,
		subscriber.WithListTopicSubscriptions(func(context.Context, *emptypb.Empty) (*rtv1.ListTopicSubscriptionsResponse, error) {
			return &rtv1.ListTopicSubscriptionsResponse{
				Subscriptions: []*rtv1.TopicSubscription{
					{
						PubsubName: "mypub",
						Topic:      "type",
						Routes: &rtv1.TopicRoutes{
							Default: "/aaa",
							Rules: []*rtv1.TopicRule{
								{Path: "/type", Match: `event.type == "com.dapr.event.sent"`},
								{Path: "/foo", Match: ""},
								{Path: "/bar", Match: `event.type == "com.dapr.event.recv"`},
							},
						},
					},
					{
						PubsubName: "mypub",
						Topic:      "order1",
						Routes: &rtv1.TopicRoutes{
							Default: "/aaa",
							Rules: []*rtv1.TopicRule{
								{Path: "/type", Match: `event.type == "com.dapr.event.sent"`},
								{Path: "/topic", Match: `event.topic == "order1"`},
							},
						},
					},
					{
						PubsubName: "mypub",
						Topic:      "order2",
						Routes: &rtv1.TopicRoutes{
							Default: "/aaa",
							Rules: []*rtv1.TopicRule{
								{Path: "/topic", Match: `event.topic == "order2"`},
								{Path: "/type", Match: `event.type == "com.dapr.event.sent"`},
							},
						},
					},
					{
						PubsubName: "mypub",
						Topic:      "order3",
						Routes: &rtv1.TopicRoutes{
							Default: "/aaa",
							Rules: []*rtv1.TopicRule{
								{Path: "/123", Match: `event.topic == "order3"`},
								{Path: "/456", Match: `event.topic == "order3"`},
							},
						},
					},
					{
						PubsubName: "mypub",
						Topic:      "order4",
						Routes: &rtv1.TopicRoutes{
							Rules: []*rtv1.TopicRule{
								{Path: "/123", Match: `event.topic == "order5"`},
								{Path: "/456", Match: `event.topic == "order6"`},
							},
						},
					},
					{
						PubsubName: "mypub",
						Topic:      "order7",
						Routes: &rtv1.TopicRoutes{
							Default: "/order7def",
							Rules: []*rtv1.TopicRule{
								{Path: "/order7rule", Match: ""},
							},
						},
					},
				},
			}, nil
		}),
	)

	m.daprd = daprd.New(t,
		daprd.WithAppPort(m.sub.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithResourceFiles(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypub
spec:
  type: pubsub.in-memory
  version: v1
`))

	return []framework.Option{
		framework.WithProcesses(m.sub, m.daprd),
	}
}

func (m *match) Run(t *testing.T, ctx context.Context) {
	m.daprd.WaitUntilRunning(t, ctx)

	client := m.daprd.GRPCClient(t, ctx)

	_, err := client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "type",
	})
	require.NoError(t, err)
	resp := m.sub.Receive(t, ctx)
	assert.Equal(t, "/type", resp.GetPath())

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "order1",
	})
	require.NoError(t, err)
	resp = m.sub.Receive(t, ctx)
	assert.Equal(t, "/type", resp.GetPath())

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "order2",
	})
	require.NoError(t, err)
	resp = m.sub.Receive(t, ctx)
	assert.Equal(t, "/topic", resp.GetPath())

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "order3",
	})
	require.NoError(t, err)
	resp = m.sub.Receive(t, ctx)
	assert.Equal(t, "/123", resp.GetPath())

	m.sub.ExpectPublishNoReceive(t, ctx, m.daprd, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "order4",
	})

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "order7",
	})
	require.NoError(t, err)
	resp = m.sub.Receive(t, ctx)
	assert.Equal(t, "/order7rule", resp.GetPath())
}
