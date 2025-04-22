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
	"fmt"
	"strconv"
	"sync/atomic"
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

	messageCount := 100000
	sendCount := atomic.Int32{}
	sendCount.Store(0)
	receivedCount := atomic.Int32{}
	receivedCount.Store(0)

	sentMessages := make(chan int, messageCount)
	receivedMessages := make(chan int, messageCount)

	go func(c *testing.T) {
		for msgID := range messageCount {
			_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
				PubsubName:      "mypub",
				Topic:           "ordered",
				Data:            []byte(strconv.Itoa(msgID)), // send Int as bytes
				DataContentType: "text/plain",
			})
			assert.NoError(c, err)
			sendCount.Add(1)
			sentMessages <- msgID
		}
	}(t)

	errCh := make(chan error)

	go func(c *testing.T) {
		for range messageCount {
			event, recvErr := stream.Recv()
			if recvErr != nil {
				errCh <- fmt.Errorf("failed to receive message: %w", recvErr)
				return
			}

			data := string(event.GetEventMessage().GetData())
			msgID, err := strconv.Atoi(data)
			if err != nil {
				errCh <- fmt.Errorf("failed to parse message ID from data: %w", err)
				return
			}

			sendErr := stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
				SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_EventProcessed{
					EventProcessed: &rtv1.SubscribeTopicEventsRequestProcessedAlpha1{
						Id:     event.GetEventMessage().GetId(),
						Status: &rtv1.TopicEventResponse{Status: rtv1.TopicEventResponse_SUCCESS},
					},
				},
			})
			if sendErr != nil {
				errCh <- fmt.Errorf("failed to send message: %w", sendErr)
				return
			}
			receivedCount.Add(1)
			receivedMessages <- msgID
		}
		errCh <- nil
	}(t)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.EqualValues(c, messageCount, sendCount.Load())
		assert.EqualValues(c, messageCount, receivedCount.Load())
	}, time.Second*60, time.Millisecond*100)

	for i := range messageCount {
		assert.Equal(t, i, <-sentMessages)
		assert.Equal(t, i, <-receivedMessages)
	}

	require.NoError(t, <-errCh)

	require.NoError(t, stream.CloseSend())
}
