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
	suite.Register(new(bulk))
}

type bulk struct {
	daprd *daprd.Daprd
	sub   *subscriber.Subscriber
}

func (b *bulk) Setup(t *testing.T) []framework.Option {
	b.sub = subscriber.New(t,
		subscriber.WithListTopicSubscriptions(func(context.Context, *emptypb.Empty) (*rtv1.ListTopicSubscriptionsResponse, error) {
			return &rtv1.ListTopicSubscriptionsResponse{
				Subscriptions: []*rtv1.TopicSubscription{
					{
						PubsubName: "mypub",
						Topic:      "a",
						Routes: &rtv1.TopicRoutes{
							Default: "/a",
						},
						BulkSubscribe: &rtv1.BulkSubscribeConfig{
							Enabled:            true,
							MaxMessagesCount:   100,
							MaxAwaitDurationMs: 40,
						},
					},
					{
						PubsubName: "mypub",
						Topic:      "b",
						Routes: &rtv1.TopicRoutes{
							Default: "/b",
						},
					},
				},
			}, nil
		}),
	)

	b.daprd = daprd.New(t,
		daprd.WithAppPort(b.sub.Port(t)),
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
		framework.WithProcesses(b.sub, b.daprd),
	}
}

func (b *bulk) Run(t *testing.T, ctx context.Context) {
	b.daprd.WaitUntilRunning(t, ctx)

	client := b.daprd.GRPCClient(t, ctx)

	// TODO: @joshvanl: add support for bulk publish to in-memory pubsub.
	resp, err := client.BulkPublishEventAlpha1(ctx, &rtv1.BulkPublishRequest{
		PubsubName: "mypub",
		Topic:      "a",
		Entries: []*rtv1.BulkPublishRequestEntry{
			{EntryId: "1", Event: []byte(`{"id": 1}`), ContentType: "application/json"},
			{EntryId: "2", Event: []byte(`{"id": 2}`), ContentType: "application/json"},
			{EntryId: "3", Event: []byte(`{"id": 3}`), ContentType: "application/json"},
			{EntryId: "4", Event: []byte(`{"id": 4}`), ContentType: "application/json"},
		},
	})
	require.NoError(t, err)
	assert.Empty(t, len(resp.GetFailedEntries()))
	b.sub.ReceiveBulk(t, ctx)
	b.sub.ReceiveBulk(t, ctx)
	b.sub.ReceiveBulk(t, ctx)
	b.sub.ReceiveBulk(t, ctx)
	b.sub.AssertBulkEventChanLen(t, 0)

	resp, err = client.BulkPublishEventAlpha1(ctx, &rtv1.BulkPublishRequest{
		PubsubName: "mypub",
		Topic:      "b",
		Entries: []*rtv1.BulkPublishRequestEntry{
			{EntryId: "1", Event: []byte(`{"id": 1}`), ContentType: "application/json"},
			{EntryId: "2", Event: []byte(`{"id": 2}`), ContentType: "application/json"},
			{EntryId: "3", Event: []byte(`{"id": 3}`), ContentType: "application/json"},
			{EntryId: "4", Event: []byte(`{"id": 4}`), ContentType: "application/json"},
		},
	})
	require.NoError(t, err)
	assert.Empty(t, len(resp.GetFailedEntries()))
	b.sub.AssertBulkEventChanLen(t, 0)
}
