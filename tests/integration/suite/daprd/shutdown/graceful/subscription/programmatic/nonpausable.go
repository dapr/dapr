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
	suite.Register(new(nonPausable))
}

// nonPausable verifies the close-first shutdown path: the pluggable server
// returns Unimplemented from Pause, the runtime falls back silently to
// close-first, and the in-flight handler still drains via the post-seal
// inflight wait. Mirrors pausable.go but with a non-pausable broker.
type nonPausable struct {
	daprd  *daprd.Daprd
	broker *broker.Broker

	inInvoke    atomic.Bool
	closeInvoke chan struct{}
}

func (p *nonPausable) Setup(t *testing.T) []framework.Option {
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
			// Required: only block-shutdown invokes StopAllSubscriptionsForever.
			daprd.WithDaprBlockShutdownDuration("10s"),
		)...,
	)

	return []framework.Option{
		framework.WithProcesses(app, p.broker),
	}
}

func (p *nonPausable) Run(t *testing.T, ctx context.Context) {
	p.daprd.Run(t, ctx)
	p.daprd.WaitUntilRunning(t, ctx)
	t.Cleanup(func() { p.daprd.Cleanup(t) })

	assert.Len(t, p.daprd.GetMetaSubscriptions(t, ctx), 1)

	// Publish; handler blocks on closeInvoke so the message is in flight
	// when shutdown begins.
	ackCh := p.broker.PublishHelloWorld("a")
	require.Eventually(t, p.inInvoke.Load, time.Second*10, time.Millisecond*10)

	cleanupDone := make(chan struct{})
	go func() {
		p.daprd.Cleanup(t)
		close(cleanupDone)
	}()

	// Pause IS still invoked by the runtime — it's the broker that
	// returns Unimplemented. The runtime treats that as ErrPausableUnimplemented
	// and falls back silently to close-first; nothing is paused.
	require.Eventually(t, func() bool {
		return p.broker.PauseCalled() >= 1
	}, time.Second*10, time.Millisecond*10,
		"runtime should attempt Pause even on a non-pausable component")
	assert.False(t, p.broker.IsPaused(),
		"broker should not be in paused state after Unimplemented response")

	// Post-seal wait holds Stop until the in-flight handler completes;
	// no ack should arrive before we release closeInvoke.
	select {
	case req := <-ackCh:
		assert.Fail(t, "unexpected ack returned before handler completed", req)
	case <-time.After(time.Second * 2):
	}

	close(p.closeInvoke)

	// Handler completes; ack flows back. Close-first path still preserves
	// the in-flight message via the post-seal inflight wait.
	select {
	case req := <-ackCh:
		assert.Nil(t, req.GetAckError(),
			"in-flight message should not be NACK'd on close-first path")
		assert.Equal(t, "foo", req.GetAckMessageId(),
			"in-flight message should be ACK'd to the broker")
	case <-time.After(time.Second * 10):
		assert.Fail(t, "timeout waiting for ack on close-first path")
	}

	// Wait for daprd to fully exit before the post-shutdown publish.
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
