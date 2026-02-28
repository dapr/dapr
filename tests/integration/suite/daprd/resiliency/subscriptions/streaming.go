/*
Copyright 2026 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package subscriptions

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
	suite.Register(new(streaming))
}

type streaming struct {
	daprd *daprd.Daprd
}

func (s *streaming) Setup(t *testing.T) []framework.Option {
	s.daprd = daprd.New(t,
		daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: dapr-resiliency
spec:
  policies:
    retries:
      retryAppCall:
        duration: 10ms
        maxRetries: 4
        policy: constant
  targets:
    components:
      mypub:
        inbound:
          retry: retryAppCall
`,
			`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypub
spec:
  type: pubsub.in-memory
  version: v1
`,
		),
	)

	return []framework.Option{
		framework.WithProcesses(s.daprd),
	}
}

func (s *streaming) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)

	client := s.daprd.GRPCClient(t, ctx)

	stream, err := client.SubscribeTopicEventsAlpha1(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_InitialRequest{
			InitialRequest: &rtv1.SubscribeTopicEventsRequestInitialAlpha1{
				PubsubName: "mypub", Topic: "a",
			},
		},
	}))

	_, err = stream.Recv()
	require.NoError(t, err)

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub", Topic: "a",
		Data:            []byte(`{"status": "completed"}`),
		DataContentType: "application/json",
	})
	require.NoError(t, err)

	for range 5 {
		resp, err := stream.Recv()
		require.NoError(t, err)
		event := resp.GetEventMessage()
		assert.Equal(t, "a", event.GetTopic())
		assert.Equal(t, "mypub", event.GetPubsubName())
		assert.JSONEq(t, `{"status": "completed"}`, string(event.GetData()))
		assert.Equal(t, "/", event.GetPath())

		require.NoError(t, stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
			SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_EventProcessed{
				EventProcessed: &rtv1.SubscribeTopicEventsRequestProcessedAlpha1{
					Id:     event.GetId(),
					Status: &rtv1.TopicEventResponse{Status: rtv1.TopicEventResponse_RETRY},
				},
			},
		}))
	}

	gotRecv := make(chan struct{})
	go func() {
		stream.Recv()
		close(gotRecv)
	}()

	select {
	case <-gotRecv:
		require.Fail(t, "expected no more messages")
	case <-time.After(time.Second):
	}

	require.NoError(t, stream.CloseSend())
}
