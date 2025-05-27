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

package stream

import (
	"context"
	serrors "errors"
	"strconv"
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
	suite.Register(new(ordering))
}

type ordering struct {
	daprd *daprd.Daprd
}

func (o *ordering) Setup(t *testing.T) []framework.Option {
	app := app.New(t)
	o.daprd = daprd.New(t,
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
		framework.WithProcesses(app, o.daprd),
	}
}

func (o *ordering) Run(t *testing.T, ctx context.Context) {
	o.daprd.WaitUntilRunning(t, ctx)

	client := o.daprd.GRPCClient(t, ctx)

	stream, err := client.SubscribeTopicEventsAlpha1(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_InitialRequest{
			InitialRequest: &rtv1.SubscribeTopicEventsRequestInitialAlpha1{
				PubsubName: "mypub",
				Topic:      "ordered",
			},
		},
	}))

	resp, err := stream.Recv()
	require.NoError(t, err)
	switch resp.GetSubscribeTopicEventsResponseType().(type) {
	case *rtv1.SubscribeTopicEventsResponseAlpha1_InitialResponse:
	default:
		require.Failf(t, "unexpected response", "got (%T) %v", resp.GetSubscribeTopicEventsResponseType(), resp)
	}

	var subsInMeta []daprd.MetadataResponsePubsubSubscription
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		subsInMeta = o.daprd.GetMetaSubscriptions(c, ctx)
		assert.Len(c, subsInMeta, 1)
	}, time.Second*10, time.Millisecond*10)

	n := 1000
	errs := make([]error, n)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range n {
			_, err := client.PublishEvent(ctx, &rtv1.PublishEventRequest{
				PubsubName:      "mypub",
				Topic:           "ordered",
				Data:            []byte(strconv.Itoa(i)),
				DataContentType: "text/plain",
			})
			errs[i] = err
		}
	}()

	for i := range n {
		event, err := stream.Recv()
		require.NoError(t, err)
		assert.Equal(t, "ordered", event.GetEventMessage().GetTopic())
		assert.Equal(t, []byte(strconv.Itoa(i)), event.GetEventMessage().GetData())

		require.NoError(t, stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
			SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_EventProcessed{
				EventProcessed: &rtv1.SubscribeTopicEventsRequestProcessedAlpha1{
					Id:     event.GetEventMessage().GetId(),
					Status: &rtv1.TopicEventResponse{Status: rtv1.TopicEventResponse_SUCCESS},
				},
			},
		}))
	}

	select {
	case <-done:
	case <-time.After(time.Second * 10):
		require.Fail(t, "timeout waiting for publish to finish")
	}

	require.NoError(t, serrors.Join(errs...))
	require.NoError(t, stream.CloseSend())
}
