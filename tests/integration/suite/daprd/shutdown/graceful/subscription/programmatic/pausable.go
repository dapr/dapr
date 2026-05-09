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

// pausable verifies the runtime's pause-and-drain shutdown path: Pause
// is invoked, in-flight messages drain to the app, post-shutdown
// publishes get neither ack nor nack.
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
	p.broker = broker.New(t, broker.WithPausable())

	p.daprd = daprd.New(t,
		p.broker.DaprdOptions(t, "mypub",
			daprd.WithAppPort(app.Port()),
			// Required: only block-shutdown invokes StopAllSubscriptionsForever.
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

	// Publish; handler blocks on closeInvoke so the message is in
	// flight when shutdown begins.
	ackCh := p.broker.PublishHelloWorld("a")
	require.Eventually(t, p.inInvoke.Load, time.Second*10, time.Millisecond*10)

	cleanupDone := make(chan struct{})
	go func() {
		p.daprd.Cleanup(t)
		close(cleanupDone)
	}()

	select {
	case <-p.broker.PauseStarted():
	case <-time.After(time.Second * 10):
		t.Fatal("Pause was not invoked on the pluggable component during graceful shutdown")
	}
	assert.GreaterOrEqual(t, p.broker.PauseCalled(), int64(1),
		"Pause should be called at least once on graceful shutdown")

	// Drain is waiting for the in-flight handler; no ack yet.
	select {
	case req := <-ackCh:
		assert.Fail(t, "unexpected ack returned before handler completed", req)
	case <-time.After(time.Second * 2):
	}

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

	// Wait for daprd to fully exit before the post-shutdown publish, so
	// the still-open PullMessages stream cannot race-deliver it.
	// Block-shutdown is configured at 10s, plus a small buffer.
	select {
	case <-cleanupDone:
	case <-time.After(time.Second * 12):
		t.Fatal("daprd did not finish shutting down within block-shutdown window")
	}

	// Post-shutdown publish: stream is closed, no ack/nack should fire.
	ackCh = p.broker.PublishHelloWorld("a")
	select {
	case req := <-ackCh:
		assert.Failf(t, "expected no ack/nack after daprd shutdown", "got: %v", req)
	case <-time.After(time.Second * 3):
	}
}
