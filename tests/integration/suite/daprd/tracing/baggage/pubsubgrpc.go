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

package baggage

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
	grpcapp "github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(pubsubGRPC))
}

// pubsubGRPC verifies that tracestate and baggage metadata set on a publish
// request are restored on the gRPC subscriber's delivery metadata.
type pubsubGRPC struct {
	daprd *daprd.Daprd
	ch    chan metadata.MD
}

func (p *pubsubGRPC) Setup(t *testing.T) []framework.Option {
	p.ch = make(chan metadata.MD, 1)

	app := grpcapp.New(t,
		grpcapp.WithOnTopicEventFn(func(ctx context.Context, in *rtv1.TopicEventRequest) (*rtv1.TopicEventResponse, error) {
			md, ok := metadata.FromIncomingContext(ctx)
			if !ok {
				md = metadata.MD{}
			}
			p.ch <- md
			return &rtv1.TopicEventResponse{Status: rtv1.TopicEventResponse_SUCCESS}, nil
		}),
		grpcapp.WithListTopicSubscriptions(func(context.Context, *emptypb.Empty) (*rtv1.ListTopicSubscriptionsResponse, error) {
			return &rtv1.ListTopicSubscriptionsResponse{
				Subscriptions: []*rtv1.TopicSubscription{
					{
						PubsubName: "mypub",
						Topic:      "test-topic",
						Routes:     &rtv1.TopicRoutes{Default: "/test-topic"},
					},
				},
			}, nil
		}),
	)

	p.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port(t)),
		daprd.WithAppProtocol("grpc"),
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
		framework.WithProcesses(app, p.daprd),
	}
}

func (p *pubsubGRPC) Run(t *testing.T, ctx context.Context) {
	p.daprd.WaitUntilRunning(t, ctx)
	grpcClient := p.daprd.GRPCClient(t, ctx)

	pubCtx := metadata.AppendToOutgoingContext(ctx,
		"traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-02",
		"tracestate", "vendor=value",
		"baggage", "key1=value1,key2=value2",
	)

	_, err := grpcClient.PublishEvent(pubCtx, &rtv1.PublishEventRequest{
		PubsubName:      "mypub",
		Topic:           "test-topic",
		Data:            []byte(`{"message": "hello"}`),
		DataContentType: "application/json",
	})
	require.NoError(t, err)

	select {
	case md := <-p.ch:
		tracestate := md.Get("tracestate")
		require.NotEmpty(t, tracestate)
		assert.Equal(t, "vendor=value", tracestate[0])

		baggageVal := md.Get("baggage")
		require.NotEmpty(t, baggageVal)
		assert.Equal(t, "key1=value1,key2=value2", baggageVal[0])
	case <-time.After(time.Second * 10):
		assert.Fail(t, "timed out waiting for pubsub event to be delivered to app")
	}
}
