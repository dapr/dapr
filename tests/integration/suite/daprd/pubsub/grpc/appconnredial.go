/*
Copyright 2026 The Dapr Authors
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

package grpc

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/listener"
	"github.com/dapr/dapr/tests/integration/framework/log"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	procgrpc "github.com/dapr/dapr/tests/integration/framework/process/grpc"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(appconnredial))
}

type appconnredial struct {
	daprd     *daprd.Daprd
	listener  *listener.Stall
	logs      *log.Log
	topicChan chan string
}

func (a *appconnredial) Setup(t *testing.T) []framework.Option {
	a.topicChan = make(chan string, 10)
	a.logs = log.New()
	a.listener = listener.New(ports.Reserve(t, 1).Listener(t))

	srv := app.New(t,
		app.WithGRPCOptions(procgrpc.WithListener(func() (net.Listener, error) {
			return a.listener, nil
		})),
		app.WithOnTopicEventFn(func(_ context.Context, in *rtv1.TopicEventRequest) (*rtv1.TopicEventResponse, error) {
			a.topicChan <- in.GetPath()
			return new(rtv1.TopicEventResponse), nil
		}),
		app.WithListTopicSubscriptions(func(context.Context, *emptypb.Empty) (*rtv1.ListTopicSubscriptionsResponse, error) {
			return &rtv1.ListTopicSubscriptionsResponse{
				Subscriptions: []*rtv1.TopicSubscription{
					{PubsubName: "mypubsub", Topic: "mytopic", Routes: &rtv1.TopicRoutes{Default: "/myroute"}},
				},
			}, nil
		}),
	)

	a.daprd = daprd.New(t,
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithExecOptions(exec.WithStdout(a.logs), exec.WithStderr(a.logs)),
		daprd.WithResourceFiles(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypubsub
spec:
  type: pubsub.in-memory
  version: v1`),
	)

	return []framework.Option{
		framework.WithProcesses(srv, a.daprd),
	}
}

func (a *appconnredial) Run(t *testing.T, ctx context.Context) {
	a.daprd.WaitUntilRunning(t, ctx)

	client := a.daprd.GRPCClient(t, ctx)

	publish := func() {
		_, err := client.PublishEvent(ctx, &rtv1.PublishEventRequest{
			PubsubName: "mypubsub",
			Topic:      "mytopic",
			Data:       []byte(`{"status": "completed"}`),
		})
		require.NoError(t, err)
	}

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		publish()
		select {
		case path := <-a.topicChan:
			assert.Equal(c, "/myroute", path)
		case <-time.After(time.Second):
			assert.Fail(c, "message not delivered")
		}
	}, time.Second*10, time.Millisecond*100)

	a.listener.SetStall(time.Second * 2)
	a.listener.CloseAccepted()

	for len(a.topicChan) > 0 {
		<-a.topicChan
	}

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		publish()
		select {
		case path := <-a.topicChan:
			assert.Equal(c, "/myroute", path)
		case <-time.After(time.Second * 3):
			assert.Fail(c, "message not delivered after re-dialing the app")
		}
	}, time.Second*30, time.Millisecond*100)

	assert.False(t, a.logs.Contains("error reading server preface"),
		"app connection was closed mid handshake")
}
