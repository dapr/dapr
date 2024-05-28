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
			InitialRequest: &rtv1.SubscribeTopicEventsInitialRequestAlpha1{
				PubsubName: "mypub", Topic: "a",
			},
		},
	}))
	t.Cleanup(func() {
		require.NoError(t, stream.CloseSend())
	})

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, b.daprd.GetMetaSubscriptions(c, ctx), 1)
	}, time.Second*10, time.Millisecond*10)

	errCh := make(chan error, 8)
	go func() {
		for i := 0; i < 4; i++ {
			event, serr := stream.Recv()
			errCh <- serr
			errCh <- stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
				SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_EventResponse{
					EventResponse: &rtv1.SubscribeTopicEventsResponseAlpha1{
						Id:     event.GetId(),
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
	assert.Empty(t, len(resp.GetFailedEntries()))

	for i := 0; i < 8; i++ {
		require.NoError(t, <-errCh)
	}
}
