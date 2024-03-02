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
	suite.Register(new(emptymatch))
}

type emptymatch struct {
	daprd *daprd.Daprd
	sub   *subscriber.Subscriber
}

func (e *emptymatch) Setup(t *testing.T) []framework.Option {
	e.sub = subscriber.New(t,
		subscriber.WithListTopicSubscriptions(func(context.Context, *emptypb.Empty) (*rtv1.ListTopicSubscriptionsResponse, error) {
			return &rtv1.ListTopicSubscriptionsResponse{
				Subscriptions: []*rtv1.TopicSubscription{
					{
						PubsubName: "mypub",
						Topic:      "justpath",
						Routes: &rtv1.TopicRoutes{
							Rules: []*rtv1.TopicRule{
								{Path: "/justpath", Match: ""},
							},
						},
					},
					{
						PubsubName: "mypub",
						Topic:      "defaultandpath",
						Routes: &rtv1.TopicRoutes{
							Default: "/abc",
							Rules: []*rtv1.TopicRule{
								{Path: "/123", Match: ""},
							},
						},
					},
					{
						PubsubName: "mypub",
						Topic:      "multipaths",
						Routes: &rtv1.TopicRoutes{
							Rules: []*rtv1.TopicRule{
								{Path: "/xyz", Match: ""},
								{Path: "/456", Match: ""},
								{Path: "/789", Match: ""},
							},
						},
					},
					{
						PubsubName: "mypub",
						Topic:      "defaultandpaths",
						Routes: &rtv1.TopicRoutes{
							Default: "/def",
							Rules: []*rtv1.TopicRule{
								{Path: "/zyz", Match: ""},
								{Path: "/aaa", Match: ""},
								{Path: "/bbb", Match: ""},
							},
						},
					},
				},
			}, nil
		}),
	)

	e.daprd = daprd.New(t,
		daprd.WithAppPort(e.sub.Port(t)),
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
		framework.WithProcesses(e.sub, e.daprd),
	}
}

func (e *emptymatch) Run(t *testing.T, ctx context.Context) {
	e.daprd.WaitUntilRunning(t, ctx)

	client := e.daprd.GRPCClient(t, ctx)

	_, err := client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "justpath",
	})
	require.NoError(t, err)
	resp := e.sub.Receive(t, ctx)
	assert.Equal(t, "/justpath", resp.GetPath())

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "defaultandpath",
	})
	require.NoError(t, err)
	resp = e.sub.Receive(t, ctx)
	assert.Equal(t, "/123", resp.GetPath())

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "multipaths",
	})
	require.NoError(t, err)
	resp = e.sub.Receive(t, ctx)
	assert.Equal(t, "/xyz", resp.GetPath())

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "defaultandpaths",
	})
	require.NoError(t, err)
	resp = e.sub.Receive(t, ctx)
	assert.Equal(t, "/zyz", resp.GetPath())
}
