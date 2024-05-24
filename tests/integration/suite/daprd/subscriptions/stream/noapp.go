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
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(noapp))
}

type noapp struct {
	daprd *daprd.Daprd
}

func (n *noapp) Setup(t *testing.T) []framework.Option {
	n.daprd = daprd.New(t,
		daprd.WithResourceFiles(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypub
spec:
  type: pubsub.in-memory
  version: v1
`))

	return []framework.Option{
		framework.WithProcesses(n.daprd),
	}
}

func (n *noapp) Run(t *testing.T, ctx context.Context) {
	n.daprd.WaitUntilRunning(t, ctx)

	client := n.daprd.GRPCClient(t, ctx)

	stream, err := client.SubscribeTopicEventsAlpha1(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_InitialRequest{
			InitialRequest: &rtv1.SubscribeTopicEventsInitialRequestAlpha1{
				PubsubName: "mypub", Topic: "a",
			},
		},
	}))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, n.daprd.GetMetaSubscriptions(c, ctx), 1)
	}, time.Second*10, time.Millisecond*10)

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub", Topic: "a",
		Data:            []byte(`{"status": "completed"}`),
		DataContentType: "application/json",
	})
	require.NoError(t, err)

	event, err := stream.Recv()
	require.NoError(t, err)
	assert.Equal(t, "a", event.GetTopic())
	assert.Equal(t, "mypub", event.GetPubsubName())
	assert.JSONEq(t, `{"status": "completed"}`, string(event.GetData()))

	require.NoError(t, stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_EventResponse{
			EventResponse: &rtv1.SubscribeTopicEventsResponseAlpha1{
				Id:     event.GetId(),
				Status: &rtv1.TopicEventResponse{Status: rtv1.TopicEventResponse_SUCCESS},
			},
		},
	}))

	require.NoError(t, stream.CloseSend())
}
