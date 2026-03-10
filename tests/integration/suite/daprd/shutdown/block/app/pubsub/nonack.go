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

package pubsub

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/pubsub/broker"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(nonack))
}

// nonack ensures that multiple messages published to a pluggable broker
// after the subscription is closed during block-shutdown are all held without
// being NACKed. This prevents messages from being incorrectly routed to a
// dead-letter queue during graceful shutdown.
type nonack struct {
	daprd  *daprd.Daprd
	broker *broker.Broker

	inInvoke    atomic.Bool
	closeInvoke chan struct{}
	healthz     atomic.Bool
}

func (n *nonack) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	n.closeInvoke = make(chan struct{})
	n.healthz.Store(true)

	app := app.New(t,
		app.WithHealthCheckFn(func(context.Context, *emptypb.Empty) (*rtv1.HealthCheckResponse, error) {
			if n.healthz.Load() {
				return new(rtv1.HealthCheckResponse), nil
			}
			return nil, errors.New("not healthy")
		}),
		app.WithOnTopicEventFn(func(context.Context, *rtv1.TopicEventRequest) (*rtv1.TopicEventResponse, error) {
			n.inInvoke.Store(true)
			<-n.closeInvoke
			return nil, nil
		}),
	)

	n.broker = broker.New(t)

	n.daprd = daprd.New(t,
		n.broker.DaprdOptions(t, "mypub",
			daprd.WithDaprBlockShutdownDuration("180s"),
			daprd.WithAppHealthProbeInterval(1),
			daprd.WithAppHealthProbeThreshold(1),
			daprd.WithAppHealthCheck(true),
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
		framework.WithProcesses(app, n.broker),
	}
}

func (n *nonack) Run(t *testing.T, ctx context.Context) {
	n.daprd.Run(t, ctx)
	n.daprd.WaitUntilRunning(t, ctx)
	t.Cleanup(func() { n.daprd.Cleanup(t) })

	assert.Len(t, n.daprd.GetMetaSubscriptions(t, ctx), 1)

	// Publish first message, handler will block.
	ch := n.broker.PublishHelloWorld("a")

	require.Eventually(t, n.inInvoke.Load, time.Second*10, time.Millisecond*10)

	// Start shutdown.
	go n.daprd.Cleanup(t)

	// First message should not be ack'd yet (handler is blocked).
	select {
	case req := <-ch:
		assert.Fail(t, "unexpected request returned", req)
	case <-time.After(time.Second * 1):
	}

	// Release the in-flight message.
	close(n.closeInvoke)

	// First message should be ACK'd successfully.
	select {
	case req := <-ch:
		assert.Nil(t, req.GetAckError())
		assert.Equal(t, "foo", req.GetAckMessageId())
	case <-time.After(time.Second * 10):
		assert.Fail(t, "timeout waiting for first message ack")
	}

	// After the in-flight message completes, the subscription is now closed.
	// Publish a second message — it should NOT be NACKed. The handler should
	// block until the subscription context is cancelled, so no ack is returned.
	ch = n.broker.PublishHelloWorld("a")
	select {
	case req := <-ch:
		assert.Fail(t, "expected no ack/nack for 2nd message, got: %v", req)
	case <-time.After(time.Second * 1):
	}

	// Make the app unhealthy to end the block-shutdown period.
	n.healthz.Store(false)
}
