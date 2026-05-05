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

package programmatic

import (
	"context"
	nethttp "net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/pubsub/broker"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(pausable))
}

// pausable verifies the runtime's pause-and-drain shutdown path on a
// pluggable pubsub component that implements PausableSubscriber. It
// asserts that on graceful shutdown:
//   - The runtime invokes Pause on the component.
//   - A message that was already in flight when shutdown started is
//     delivered to the app and ack'd to the broker (drain-to-app).
//   - A message that arrives after the drain has sealed is neither
//     ack'd nor nack'd (matches the close-first contract for messages
//     arriving post-shutdown).
type pausable struct {
	daprd  *daprd.Daprd
	broker *broker.Broker

	inInvoke    atomic.Bool
	closeInvoke chan struct{}
}

func (p *pausable) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	app := app.New(t,
		app.WithSubscribe(`[{"pubsubname":"mypub","topic":"a","route":"/a"}]`),
		app.WithHandlerFunc("/a", func(nethttp.ResponseWriter, *nethttp.Request) {
			p.inInvoke.Store(true)
			<-p.closeInvoke
		}),
	)

	p.closeInvoke = make(chan struct{})
	p.broker = broker.New(t)

	p.daprd = daprd.New(t,
		p.broker.DaprdOptions(t, "mypub",
			daprd.WithAppPort(app.Port()),
			// Block-shutdown-duration is what triggers
			// StopAllSubscriptionsForever in the runtime; without
			// it, subscriptions never see graceful Stop and the
			// pause-and-drain path is dead code.
			daprd.WithDaprBlockShutdownDuration("10s"),
		)...,
	)

	return []framework.Option{
		framework.WithProcesses(app, p.broker),
	}
}

func (p *pausable) Run(t *testing.T, ctx context.Context) {
	p.daprd.Run(t, ctx)
	p.daprd.WaitUntilRunning(t, ctx)
	t.Cleanup(func() { p.daprd.Cleanup(t) })

	assert.Len(t, p.daprd.GetMetaSubscriptions(t, ctx), 1)

	// Publish one message; the app handler blocks on closeInvoke so
	// the message stays in flight when shutdown begins below — exactly
	// the drain-to-app scenario.
	ackCh := p.broker.PublishHelloWorld("a")
	require.Eventually(t, p.inInvoke.Load, time.Second*10, time.Millisecond*10)

	// Trigger graceful shutdown. Stop runs synchronously inside the
	// runtime's block-shutdown closer, so we observe progress via the
	// broker side-channels.
	go p.daprd.Cleanup(t)

	select {
	case <-p.broker.PauseStarted():
	case <-time.After(time.Second * 10):
		t.Fatal("Pause was not invoked on the pluggable component during graceful shutdown")
	}
	assert.GreaterOrEqual(t, p.broker.PauseCalled(), int64(1),
		"Pause should be called at least once on graceful shutdown")

	// Stop must still be in flight — drain is waiting for the in-flight
	// handler to complete. No ack should reach the broker yet.
	select {
	case req := <-ackCh:
		assert.Fail(t, "unexpected ack returned before handler completed", req)
	case <-time.After(time.Second * 2):
	}

	// Release the handler. Drain delivers the result; ack flows back.
	close(p.closeInvoke)

	select {
	case req := <-ackCh:
		assert.Nil(t, req.GetAckError(),
			"buffered message should not be NACK'd during drain")
		assert.Equal(t, "foo", req.GetAckMessageId(),
			"buffered message should be ACK'd to the broker during drain-to-app")
	case <-time.After(time.Second * 10):
		assert.Fail(t, "timeout waiting for drain ack")
	}
}
