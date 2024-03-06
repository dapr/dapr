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

package grpc

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

func (e *match) Setup(t *testing.T) []framework.Option {
	e.sub = subscriber.New(t,
		subscriber.WithListTopicSubscriptions(func(context.Context, *emptypb.Empty) (*rtv1.ListTopicSubscriptionsResponse, error) {
			return &rtv1.ListTopicSubscriptionsResponse{
				Subscriptions: []*rtv1.TopicSubscription{
					{
						PubsubName: "mypub",
						Topic:      "a",
						Routes: &rtv1.TopicRoutes{
							Default: "/a/b/c/d",
						},
					},
					{
						PubsubName: "mypub",
						Topic:      "b",
						Routes: &rtv1.TopicRoutes{
							Rules: []*rtv1.TopicRule{
								{Path: "/123", Match: `event.topic == "b"`},
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
---
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: sub1
spec:
 pubsubname: mypub
 topic: a
 routes:
  rules:
  - path: /123
    match: event.topic == "a"
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
 name: sub2
spec:
 pubsubname: mypub
 topic: b
 route: /a/b/c/d
`))

	return []framework.Option{
		framework.WithProcesses(e.sub, e.daprd),
	}
}

func (e *match) Run(t *testing.T, ctx context.Context) {
	e.daprd.WaitUntilRunning(t, ctx)

	client := e.daprd.GRPCClient(t, ctx)

	_, err := client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub", Topic: "a", Data: []byte(`{"status": "completed"}`),
	})
	require.NoError(t, err)
	assert.Equal(t, "/a/b/c/d", e.sub.Receive(t, ctx).GetPath())

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub", Topic: "a", Data: []byte(`{"status": "completed"}`),
	})
	require.NoError(t, err)
	assert.Equal(t, "/a/b/c/d", e.sub.Receive(t, ctx).GetPath())
}
