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

package subscription

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/proto/components/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/pubsub/broker"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(streaming))
}

type streaming struct {
	daprd  *daprd.Daprd
	broker *broker.Broker
}

func (s *streaming) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	s.broker = broker.New(t)

	s.daprd = daprd.New(t,
		s.broker.DaprdOptions(t, "mypub")...,
	)

	return []framework.Option{
		framework.WithProcesses(s.broker),
	}
}

func (s *streaming) Run(t *testing.T, ctx context.Context) {
	s.daprd.Run(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)
	t.Cleanup(func() { s.daprd.Cleanup(t) })

	stream, err := s.daprd.GRPCClient(t, ctx).SubscribeTopicEventsAlpha1(ctx)
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

	assert.Len(t, s.daprd.GetMetaSubscriptions(t, ctx), 1)

	ch := s.broker.PublishHelloWorld("a")

	resp, err := stream.Recv()
	require.NoError(t, err)

	assert.Equal(t, "a", resp.GetEventMessage().GetTopic())

	go s.daprd.Cleanup(t)

	select {
	case req := <-ch:
		assert.Fail(t, "unexpected request returned", req)
	case <-time.After(time.Second * 3):
	}

	require.NoError(t, stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_EventProcessed{
			EventProcessed: &rtv1.SubscribeTopicEventsRequestProcessedAlpha1{
				Id:     resp.GetEventMessage().GetId(),
				Status: &rtv1.TopicEventResponse{Status: rtv1.TopicEventResponse_SUCCESS},
			},
		},
	}))

	select {
	case req := <-ch:
		assert.Nil(t, req.GetAckError())
		assert.Equal(t, "foo", req.GetAckMessageId())
	case <-time.After(time.Second * 10):
		assert.Fail(t, "timeout")
	}

	ch = s.broker.PublishHelloWorld("a")
	select {
	case req := <-ch:
		assert.Equal(t, &components.AckMessageError{Message: "subscription is closed"}, req.GetAckError())
		assert.Equal(t, "foo", req.GetAckMessageId())
	case <-time.After(time.Second * 10):
		assert.Fail(t, "timeout")
	}
}
