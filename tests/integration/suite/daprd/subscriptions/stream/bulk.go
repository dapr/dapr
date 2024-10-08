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

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(bulk))
}

type bulk struct {
	daprd *daprd.Daprd
}

func (b *bulk) Setup(t *testing.T) []framework.Option {
	app := app.New(t)
	b.daprd = daprd.New(t,
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(app.Port(t)),
		daprd.WithResourceFiles(`apiVersion: dapr.io/v1alpha1
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

func (b *bulk) Run(t *testing.T, ctx context.Context) {
	b.daprd.WaitUntilRunning(t, ctx)

	client := b.daprd.GRPCClient(t, ctx)

	stream, err := client.SubscribeTopicEventsAlpha1(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_InitialRequest{
			InitialRequest: &rtv1.SubscribeTopicEventsRequestInitialAlpha1{
				PubsubName: "mypub", Topic: "a",
			},
		},
	}))
	t.Cleanup(func() {
		require.NoError(t, stream.CloseSend())
	})

	sresp, err := stream.Recv()
	require.NoError(t, err)
	switch sresp.GetSubscribeTopicEventsResponseType().(type) {
	case *rtv1.SubscribeTopicEventsResponseAlpha1_InitialResponse:
	default:
		require.Fail(t, "unexpected response type")
	}

	var subsInMeta []daprd.MetadataResponsePubsubSubscription
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		subsInMeta = b.daprd.GetMetaSubscriptions(c, ctx)
		assert.Len(c, subsInMeta, 1)
	}, time.Second*10, time.Millisecond*10)
	assert.Equal(t, rtv1.PubsubSubscriptionType_STREAMING.String(), subsInMeta[0].Type)

	errCh := make(chan error, 8)
	go func() {
		for range 4 {
			event, serr := stream.Recv()
			errCh <- serr
			errCh <- stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
				SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_EventProcessed{
					EventProcessed: &rtv1.SubscribeTopicEventsRequestProcessedAlpha1{
						Id:     event.GetEventMessage().GetId(),
						Status: &rtv1.TopicEventResponse{Status: rtv1.TopicEventResponse_SUCCESS},
					},
				},
			})
		}
	}()

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
	assert.Empty(t, resp.GetFailedEntries())

	for range 8 {
		require.NoError(t, <-errCh)
	}
}
