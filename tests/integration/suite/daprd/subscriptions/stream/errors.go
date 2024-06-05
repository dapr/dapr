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
	"google.golang.org/grpc/status"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(errors))
}

type errors struct {
	daprd *daprd.Daprd
}

func (e *errors) Setup(t *testing.T) []framework.Option {
	app := app.New(t)
	e.daprd = daprd.New(t,
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
		framework.WithProcesses(app, e.daprd),
	}
}

func (e *errors) Run(t *testing.T, ctx context.Context) {
	e.daprd.WaitUntilRunning(t, ctx)

	client := e.daprd.GRPCClient(t, ctx)

	stream, err := client.SubscribeTopicEventsAlpha1(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_InitialRequest{
			InitialRequest: &rtv1.SubscribeTopicEventsInitialRequestAlpha1{
				PubsubName: "", Topic: "a",
			},
		},
	}))
	_, err = stream.Recv()
	s, ok := status.FromError(err)
	require.True(t, ok)
	assert.Contains(t, s.Message(), "pubsubName name is required")
	require.NoError(t, stream.CloseSend())

	stream, err = client.SubscribeTopicEventsAlpha1(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_InitialRequest{
			InitialRequest: &rtv1.SubscribeTopicEventsInitialRequestAlpha1{
				PubsubName: "mypub", Topic: "",
			},
		},
	}))
	_, err = stream.Recv()
	s, ok = status.FromError(err)
	require.True(t, ok)
	assert.Contains(t, s.Message(), "topic is required")
	require.NoError(t, stream.CloseSend())

	stream, err = client.SubscribeTopicEventsAlpha1(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_InitialRequest{
			InitialRequest: &rtv1.SubscribeTopicEventsInitialRequestAlpha1{
				PubsubName: "mypub", Topic: "a",
			},
		},
	}))
	t.Cleanup(func() { require.NoError(t, stream.CloseSend()) })
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, e.daprd.GetMetaSubscriptions(c, ctx), 1)
	}, time.Second*10, time.Millisecond*10)

	streamDupe, err := client.SubscribeTopicEventsAlpha1(ctx)
	require.NoError(t, err)
	require.NoError(t, streamDupe.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_InitialRequest{
			InitialRequest: &rtv1.SubscribeTopicEventsInitialRequestAlpha1{
				PubsubName: "mypub", Topic: "a",
			},
		},
	}))
	t.Cleanup(func() { require.NoError(t, streamDupe.CloseSend()) })
	_, err = streamDupe.Recv()
	s, ok = status.FromError(err)
	require.True(t, ok)
	assert.Contains(t, s.Message(), `already subscribed to pubsub "mypub" topic "a"`)

	streamDoubleInit, err := client.SubscribeTopicEventsAlpha1(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, streamDoubleInit.CloseSend()) })
	require.NoError(t, streamDoubleInit.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_InitialRequest{
			InitialRequest: &rtv1.SubscribeTopicEventsInitialRequestAlpha1{
				PubsubName: "mypub", Topic: "b",
			},
		},
	}))
	require.NoError(t, streamDoubleInit.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_InitialRequest{
			InitialRequest: &rtv1.SubscribeTopicEventsInitialRequestAlpha1{
				PubsubName: "mypub", Topic: "b",
			},
		},
	}))
	_, err = streamDoubleInit.Recv()
	s, ok = status.FromError(err)
	require.True(t, ok)
	assert.Contains(t, s.Message(), "duplicate initial request received")
}
