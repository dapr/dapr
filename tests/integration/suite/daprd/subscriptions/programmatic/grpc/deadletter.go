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
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(deadletter))
}

type deadletter struct {
	daprd *daprd.Daprd
	app   *app.App
	inCh  chan *rtv1.TopicEventRequest
}

func (d *deadletter) Setup(t *testing.T) []framework.Option {
	d.inCh = make(chan *rtv1.TopicEventRequest)
	d.app = app.New(t,
		app.WithListTopicSubscriptions(func(context.Context, *emptypb.Empty) (*rtv1.ListTopicSubscriptionsResponse, error) {
			return &rtv1.ListTopicSubscriptionsResponse{
				Subscriptions: []*rtv1.TopicSubscription{
					{
						PubsubName: "mypub",
						Topic:      "a",
						Routes: &rtv1.TopicRoutes{
							Default: "/a",
						},
						DeadLetterTopic: "mydead",
					},
					{
						PubsubName: "mypub",
						Topic:      "mydead",
						Routes: &rtv1.TopicRoutes{
							Default: "/b",
						},
						DeadLetterTopic: "mydead",
					},
				},
			}, nil
		}),
		app.WithOnTopicEventFn(func(ctx context.Context, in *rtv1.TopicEventRequest) (*rtv1.TopicEventResponse, error) {
			if in.GetTopic() == "a" {
				return nil, errors.New("my error")
			}
			d.inCh <- in
			return new(rtv1.TopicEventResponse), nil
		}),
	)

	d.daprd = daprd.New(t,
		daprd.WithAppPort(d.app.Port(t)),
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
		framework.WithProcesses(d.app, d.daprd),
	}
}

func (d *deadletter) Run(t *testing.T, ctx context.Context) {
	d.daprd.WaitUntilRunning(t, ctx)

	client := d.daprd.GRPCClient(t, ctx)

	_, err := client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub", Topic: "a", Data: []byte(`{"status": "completed"}`),
		Metadata: map[string]string{"foo": "bar"}, DataContentType: "application/json",
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(ctx, time.Second)
	t.Cleanup(cancel)
	select {
	case <-ctx.Done():
		assert.Fail(t, "timeout waiting for event")
	case in := <-d.inCh:
		assert.Equal(t, "mydead", in.GetTopic())
		assert.Equal(t, "/b", in.GetPath())
	}
}
