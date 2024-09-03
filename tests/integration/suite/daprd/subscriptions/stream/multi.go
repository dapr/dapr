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
	suite.Register(new(multi))
}

type multi struct {
	daprd *daprd.Daprd
}

func (m *multi) Setup(t *testing.T) []framework.Option {
	app := app.New(t)
	m.daprd = daprd.New(t,
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
		framework.WithProcesses(app, m.daprd),
	}
}

func (m *multi) Run(t *testing.T, ctx context.Context) {
	m.daprd.WaitUntilRunning(t, ctx)

	client := m.daprd.GRPCClient(t, ctx)

	stream1, err := client.SubscribeTopicEventsAlpha1(ctx)
	require.NoError(t, err)
	require.NoError(t, stream1.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_InitialRequest{
			InitialRequest: &rtv1.SubscribeTopicEventsRequestInitialAlpha1{
				PubsubName: "mypub", Topic: "a",
			},
		},
	}))
	resp, err := stream1.Recv()
	require.NoError(t, err)
	switch resp.GetSubscribeTopicEventsResponseType().(type) {
	case *rtv1.SubscribeTopicEventsResponseAlpha1_InitialResponse:
	default:
		require.Failf(t, "unexpected response", "got (%T) %v", resp.GetSubscribeTopicEventsResponseType(), resp)
	}

	stream2, err := client.SubscribeTopicEventsAlpha1(ctx)
	require.NoError(t, err)
	require.NoError(t, stream2.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_InitialRequest{
			InitialRequest: &rtv1.SubscribeTopicEventsRequestInitialAlpha1{
				PubsubName: "mypub", Topic: "b",
			},
		},
	}))
	resp, err = stream2.Recv()
	require.NoError(t, err)
	switch resp.GetSubscribeTopicEventsResponseType().(type) {
	case *rtv1.SubscribeTopicEventsResponseAlpha1_InitialResponse:
	default:
		require.Failf(t, "unexpected response", "got (%T) %v", resp.GetSubscribeTopicEventsResponseType(), resp)
	}

	stream3, err := client.SubscribeTopicEventsAlpha1(ctx)
	require.NoError(t, err)
	require.NoError(t, stream3.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_InitialRequest{
			InitialRequest: &rtv1.SubscribeTopicEventsRequestInitialAlpha1{
				PubsubName: "mypub", Topic: "c",
			},
		},
	}))
	resp, err = stream3.Recv()
	require.NoError(t, err)
	switch resp.GetSubscribeTopicEventsResponseType().(type) {
	case *rtv1.SubscribeTopicEventsResponseAlpha1_InitialResponse:
	default:
		require.Failf(t, "unexpected response", "got (%T) %v", resp.GetSubscribeTopicEventsResponseType(), resp)
	}

	t.Cleanup(func() {
		require.NoError(t, stream1.CloseSend())
		require.NoError(t, stream2.CloseSend())
		require.NoError(t, stream3.CloseSend())
	})

	var subsInMeta []daprd.MetadataResponsePubsubSubscription
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		subsInMeta = m.daprd.GetMetaSubscriptions(c, ctx)
		assert.Len(c, subsInMeta, 3)
	}, time.Second*5, time.Millisecond*10)
	assert.ElementsMatch(t, []daprd.MetadataResponsePubsubSubscription{
		{PubsubName: "mypub", Topic: "a", Rules: []daprd.MetadataResponsePubsubSubscriptionRule{{Path: "/"}}, Type: rtv1.PubsubSubscriptionType_STREAMING.String()},
		{PubsubName: "mypub", Topic: "c", Rules: []daprd.MetadataResponsePubsubSubscriptionRule{{Path: "/"}}, Type: rtv1.PubsubSubscriptionType_STREAMING.String()},
		{PubsubName: "mypub", Topic: "b", Rules: []daprd.MetadataResponsePubsubSubscriptionRule{{Path: "/"}}, Type: rtv1.PubsubSubscriptionType_STREAMING.String()},
	},
		subsInMeta,
	)

	for _, topic := range []string{"a", "b", "c"} {
		_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
			PubsubName: "mypub", Topic: topic,
			Data:            []byte(`{"status": "completed"}`),
			DataContentType: "application/json",
		})
		require.NoError(t, err)
	}

	for stream, topic := range map[rtv1.Dapr_SubscribeTopicEventsAlpha1Client]string{
		stream1: "a",
		stream2: "b",
		stream3: "c",
	} {
		event, err := stream.Recv()
		require.NoError(t, err)
		assert.Equal(t, topic, event.GetEventMessage().GetTopic())
	}
}
