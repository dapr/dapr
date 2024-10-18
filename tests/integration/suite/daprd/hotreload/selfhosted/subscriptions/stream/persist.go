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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/subscriber"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(persist))
}

type persist struct {
	daprd *daprd.Daprd
	sub   *subscriber.Subscriber
	dir   string
}

func (p *persist) Setup(t *testing.T) []framework.Option {
	p.sub = subscriber.New(t)

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: hotreloading
spec:
  features:
  - name: HotReload
    enabled: true`), 0o600))

	p.dir = t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(p.dir, "pubsub.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: mypub
spec:
 type: pubsub.in-memory
 version: v1
`), 0o600))

	p.daprd = daprd.New(t,
		daprd.WithAppPort(p.sub.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(p.dir),
	)

	return []framework.Option{
		framework.WithProcesses(p.sub, p.daprd),
	}
}

func (p *persist) Run(t *testing.T, ctx context.Context) {
	p.daprd.WaitUntilRunning(t, ctx)

	assert.Len(t, p.daprd.GetMetaRegisteredComponents(t, ctx), 1)
	assert.Empty(t, p.daprd.GetMetaSubscriptions(t, ctx))

	require.NoError(t, os.WriteFile(filepath.Join(p.dir, "sub.yaml"), []byte(`
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: sub
spec:
 pubsubname: mypub
 topic: a
 routes:
  default: /a
`), 0o600))
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, p.daprd.GetMetaSubscriptions(t, ctx), 1)
	}, time.Second*5, time.Millisecond*10)

	newReq := func(pubsub, topic string) *rtv1.PublishEventRequest {
		return &rtv1.PublishEventRequest{PubsubName: pubsub, Topic: topic, Data: []byte(`{"status": "completed"}`)}
	}
	p.sub.ExpectPublishReceive(t, ctx, p.daprd, newReq("mypub", "a"))

	client := p.daprd.GRPCClient(t, ctx)
	stream, err := client.SubscribeTopicEventsAlpha1(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_InitialRequest{
			InitialRequest: &rtv1.SubscribeTopicEventsRequestInitialAlpha1{
				PubsubName: "mypub", Topic: "b",
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

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, p.daprd.GetMetaSubscriptions(c, ctx), 2)
	}, time.Second*10, time.Millisecond*10)

	p.sub.ExpectPublishReceive(t, ctx, p.daprd, newReq("mypub", "a"))
	_, err = client.PublishEvent(ctx, newReq("mypub", "b"))
	require.NoError(t, err)
	event, err := stream.Recv()
	require.NoError(t, err)
	assert.Equal(t, "b", event.GetEventMessage().GetTopic())
	require.NoError(t, stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_EventProcessed{
			EventProcessed: &rtv1.SubscribeTopicEventsRequestProcessedAlpha1{
				Id:     event.GetEventMessage().GetId(),
				Status: &rtv1.TopicEventResponse{Status: rtv1.TopicEventResponse_SUCCESS},
			},
		},
	}))

	require.NoError(t, os.WriteFile(filepath.Join(p.dir, "sub.yaml"), []byte(`
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: sub
spec:
 pubsubname: mypub
 topic: a
 routes:
  default: /a
---
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: sub2
spec:
 pubsubname: mypub
 topic: c
 routes:
  default: /c
`), 0o600))
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, p.daprd.GetMetaSubscriptions(t, ctx), 2)
	}, time.Second*5, time.Millisecond*10)
}
