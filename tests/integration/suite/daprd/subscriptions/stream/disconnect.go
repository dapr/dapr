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
	suite.Register(new(disconnect))
}

type disconnect struct {
	daprd *daprd.Daprd
}

func (d *disconnect) Setup(t *testing.T) []framework.Option {
	d.daprd = daprd.New(t,
		daprd.WithResourceFiles(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypub
spec:
  type: pubsub.in-memory
  version: v1
`))

	return nil
}

func (d *disconnect) Run(t *testing.T, ctx context.Context) {
	d.daprd.Run(t, ctx)
	d.daprd.WaitUntilRunning(t, ctx)

	client := d.daprd.GRPCClient(t, ctx)

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

	var subsInMeta []daprd.MetadataResponsePubsubSubscription
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		subsInMeta = d.daprd.GetMetaSubscriptions(c, ctx)
		assert.Len(c, subsInMeta, 1)
	}, time.Second*5, time.Millisecond*10)
	assert.Equal(t, rtv1.PubsubSubscriptionType_STREAMING.String(), subsInMeta[0].Type)

	d.daprd.Cleanup(t)

	require.NoError(t, stream1.CloseSend())
	require.NoError(t, stream2.CloseSend())
}
