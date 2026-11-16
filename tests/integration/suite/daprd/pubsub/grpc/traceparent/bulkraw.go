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

package traceparent

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
	suite.Register(new(bulkraw))
}

type bulkraw struct {
	daprd *daprd.Daprd
	ch    chan metadata.MD
}

func (b *bulkraw) Setup(t *testing.T) []framework.Option {
	b.ch = make(chan metadata.MD, 2)

	app := app.New(t,
		app.WithOnTopicEventFn(func(ctx context.Context, in *rtv1.TopicEventRequest) (*rtv1.TopicEventResponse, error) {
			md, ok := metadata.FromIncomingContext(ctx)
			if !ok {
				md = metadata.MD{}
			}
			b.ch <- md
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
						Metadata: map[string]string{
							"rawPayload": rawPayload,
						},
						Routes: &rtv1.TopicRoutes{
							Default: "/test-topic",
						},
					},
				},
			}, nil
		}),
	)

	b.daprd = daprd.New(t,
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
		framework.WithProcesses(app, b.daprd),
	}
}

func (b *bulkraw) Run(t *testing.T, ctx context.Context) {
	b.daprd.WaitUntilRunning(t, ctx)
	client := b.daprd.GRPCClient(t, ctx)

	tp := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	ctxWithTraceInfo := metadata.AppendToOutgoingContext(ctx, "traceparent", tp)

	res, err := client.BulkPublishEvent(ctxWithTraceInfo, &rtv1.BulkPublishRequest{
		PubsubName: "mypub",
		Topic:      "test-topic",
		Metadata: map[string]string{
			"rawPayload": rawPayload,
		},
		Entries: []*rtv1.BulkPublishRequestEntry{
			{EntryId: "1", Event: []byte(`{"message": "one"}`), ContentType: "application/json"},
			{EntryId: "2", Event: []byte(`{"message": "two"}`), ContentType: "application/json"},
		},
	})
	require.NoError(t, err)
	require.Empty(t, res.GetFailedEntries())

	for range 2 {
		select {
		case md := <-b.ch:
			traceparent := md.Get("traceparent")
			require.Len(t, traceparent, 1)
			assert.Equal(t, tp[3:35], traceparent[0][3:35])
		case <-time.After(time.Second * 10):
			assert.Fail(t, "Timed out waiting for pubsub event to be delivered to app")
		}
	}
}
