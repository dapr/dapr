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

package declarative

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/proto/components/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/pubsub/broker"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(grpc))
}

type grpc struct {
	daprd  *daprd.Daprd
	broker *broker.Broker

	inInvoke    atomic.Bool
	closeInvoke chan struct{}
}

func (g *grpc) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	g.closeInvoke = make(chan struct{})

	app := app.New(t,
		app.WithOnTopicEventFn(func(context.Context, *rtv1.TopicEventRequest) (*rtv1.TopicEventResponse, error) {
			g.inInvoke.Store(true)
			<-g.closeInvoke
			return nil, nil
		}),
	)

	g.broker = broker.New(t)

	g.daprd = daprd.New(t,
		g.broker.DaprdOptions(t, "mypub",
			daprd.WithAppPort(app.Port(t)),
			daprd.WithAppProtocol("grpc"),
			daprd.WithResourceFiles(`
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
  name: mysub
spec:
  pubsubname: mypub
  topic: a
  routes:
    default: /a
`),
		)...,
	)

	return []framework.Option{
		framework.WithProcesses(app, g.broker),
	}
}

func (g *grpc) Run(t *testing.T, ctx context.Context) {
	g.daprd.Run(t, ctx)
	g.daprd.WaitUntilRunning(t, ctx)
	t.Cleanup(func() { g.daprd.Cleanup(t) })

	assert.Len(t, g.daprd.GetMetaSubscriptions(t, ctx), 1)

	ch := g.broker.PublishHelloWorld("a")

	require.Eventually(t, g.inInvoke.Load, time.Second*10, time.Millisecond*10)

	go g.daprd.Cleanup(t)

	select {
	case req := <-ch:
		assert.Fail(t, "unexpected request returned", req)
	case <-time.After(time.Second * 3):
	}

	close(g.closeInvoke)

	select {
	case req := <-ch:
		assert.Nil(t, req.GetAckError())
		assert.Equal(t, "foo", req.GetAckMessageId())
	case <-time.After(time.Second * 10):
		assert.Fail(t, "timeout")
	}

	ch = g.broker.PublishHelloWorld("a")
	select {
	case req := <-ch:
		assert.Equal(t, &components.AckMessageError{Message: "subscription is closed"}, req.GetAckError())
		assert.Equal(t, "foo", req.GetAckMessageId())
	case <-time.After(time.Second * 10):
		assert.Fail(t, "timeout")
	}
}
