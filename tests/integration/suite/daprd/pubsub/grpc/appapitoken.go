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

package grpc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(appapitoken))
}

type appapitoken struct {
	daprd *daprd.Daprd
	ch    chan metadata.MD
}

func (a *appapitoken) Setup(t *testing.T) []framework.Option {
	a.ch = make(chan metadata.MD, 1)

	app := app.New(t,
		app.WithOnTopicEventFn(func(ctx context.Context, in *rtv1.TopicEventRequest) (*rtv1.TopicEventResponse, error) {
			md, ok := metadata.FromIncomingContext(ctx)
			if !ok {
				md = metadata.MD{}
			}
			a.ch <- md
			return &rtv1.TopicEventResponse{
				Status: rtv1.TopicEventResponse_SUCCESS,
			}, nil
		}),
		app.WithListTopicSubscriptions(func(context.Context, *emptypb.Empty) (*rtv1.ListTopicSubscriptionsResponse, error) {
			return &rtv1.ListTopicSubscriptionsResponse{
				Subscriptions: []*rtv1.TopicSubscription{
					{
						PubsubName: "mypub",
						Topic:      "test-topic",
						Routes: &rtv1.TopicRoutes{
							Default: "/test-topic",
						},
					},
				},
			}, nil
		}),
	)

	a.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppAPIToken(t, "test-app-token"),
		daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypub
spec:
  type: pubsub.in-memory
  version: v1
`))

	return []framework.Option{
		framework.WithProcesses(app, a.daprd),
	}
}

func (a *appapitoken) Run(t *testing.T, ctx context.Context) {
	a.daprd.WaitUntilRunning(t, ctx)

	client := a.daprd.GRPCClient(t, ctx)
	_, err := client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName:      "mypub",
		Topic:           "test-topic",
		Data:            []byte(`{"message": "hello"}`),
		DataContentType: "application/json",
	})
	require.NoError(t, err)

	// Check that the app received the event with the APP_API_TOKEN in metadata
	select {
	case md := <-a.ch:
		tokens := md.Get("dapr-api-token")
		assert.NotEmpty(t, tokens)
		assert.Equal(t, "test-app-token", tokens[0])
	case <-time.After(time.Second * 10):
		assert.Fail(t, "Timed out waiting for pubsub event to be delivered to app")
	}
}
