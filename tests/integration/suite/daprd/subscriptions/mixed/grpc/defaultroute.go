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
	suite.Register(new(defaultroute))
}

type defaultroute struct {
	daprd *daprd.Daprd
	sub   *subscriber.Subscriber
}

func (d *defaultroute) Setup(t *testing.T) []framework.Option {
	d.sub = subscriber.New(t,
		subscriber.WithListTopicSubscriptions(func(context.Context, *emptypb.Empty) (*rtv1.ListTopicSubscriptionsResponse, error) {
			return &rtv1.ListTopicSubscriptionsResponse{
				Subscriptions: []*rtv1.TopicSubscription{
					{
						PubsubName: "mypub",
						Topic:      "a",
						Routes: &rtv1.TopicRoutes{
							Default: "/123",
						},
					},
					{
						PubsubName: "mypub",
						Topic:      "b",
						Routes: &rtv1.TopicRoutes{
							Default: "/xyz",
						},
					},
				},
			}, nil
		}),
	)

	d.daprd = daprd.New(t,
		daprd.WithAppPort(d.sub.Port(t)),
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
Kind: Subscription
metadata:
 name: sub1
spec:
 pubsubname: mypub
 topic: a
 routes:
  default: /456
---
apiVersion: dapr.io/v1alpha1
Kind: Subscription
metadata:
 name: sub2
spec:
 pubsubname: mypub
 topic: b
 route: /zyx
`))

	return []framework.Option{
		framework.WithProcesses(d.sub, d.daprd),
	}
}

func (d *defaultroute) Run(t *testing.T, ctx context.Context) {
	d.daprd.WaitUntilRunning(t, ctx)

	client := d.daprd.GRPCClient(t, ctx)

	_, err := client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub", Topic: "a", Data: []byte(`{"status": "completed"}`),
	})
	require.NoError(t, err)
	assert.Equal(t, "/123", d.sub.Receive(t, ctx).GetPath())

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub", Topic: "a", Data: []byte(`{"status": "completed"}`),
	})
	require.NoError(t, err)
	assert.Equal(t, "/123", d.sub.Receive(t, ctx).GetPath())
}
