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

package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	channelt "github.com/dapr/dapr/pkg/channel/testing"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/channels"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/subscription/postman/http"
)

// pausablePubSub is a mock that implements PausableSubscriber for tests.
type pausablePubSub struct {
	mu       sync.Mutex
	handlers map[string]contribpubsub.Handler

	pauseCalled  atomic.Int64
	resumeCalled atomic.Int64

	// pauseStarted is closed the first time Pause is called.
	pauseStarted chan struct{}
	pauseOnce    sync.Once

	// pauseGate, if non-nil, blocks Pause until closed.
	pauseGate chan struct{}
}

func newPausablePubSub() *pausablePubSub {
	return &pausablePubSub{
		handlers:     make(map[string]contribpubsub.Handler),
		pauseStarted: make(chan struct{}),
	}
}

func (p *pausablePubSub) Init(context.Context, contribpubsub.Metadata) error {
	return nil
}

func (p *pausablePubSub) Features() []contribpubsub.Feature {
	return nil
}

func (p *pausablePubSub) Publish(_ context.Context, req *contribpubsub.PublishRequest) error {
	p.mu.Lock()
	h := p.handlers[req.Topic]
	p.mu.Unlock()
	if h == nil {
		return nil
	}
	return h(context.Background(), &contribpubsub.NewMessage{
		Data:     req.Data,
		Topic:    req.Topic,
		Metadata: req.Metadata,
	})
}

func (p *pausablePubSub) Subscribe(_ context.Context, req contribpubsub.SubscribeRequest, handler contribpubsub.Handler) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.handlers[req.Topic] = handler
	return nil
}

func (p *pausablePubSub) Close() error { return nil }

func (p *pausablePubSub) GetComponentMetadata() (map[string]string, []string, []string) {
	return nil, nil, nil
}

// Pause implements contribpubsub.PausableSubscriber.
func (p *pausablePubSub) Pause(ctx context.Context) error {
	p.pauseCalled.Add(1)
	p.pauseOnce.Do(func() { close(p.pauseStarted) })
	if p.pauseGate != nil {
		select {
		case <-p.pauseGate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// deliver invokes the captured Subscribe handler for the given topic,
// simulating a contrib consumer goroutine pulling a buffered message.
func (p *pausablePubSub) deliver(ctx context.Context, topic string, data []byte) error {
	p.mu.Lock()
	h := p.handlers[topic]
	p.mu.Unlock()
	if h == nil {
		return errors.New("no handler registered for topic")
	}
	return h(ctx, &contribpubsub.NewMessage{Data: data, Topic: topic})
}

// Resume implements contribpubsub.PausableSubscriber.
func (p *pausablePubSub) Resume(context.Context) error {
	p.resumeCalled.Add(1)
	return nil
}

// Compile-time assertion that the mock implements the interface.
var _ contribpubsub.PausableSubscriber = (*pausablePubSub)(nil)

// nonPausablePubSub intentionally omits PausableSubscriber.
type nonPausablePubSub struct {
	mu       sync.Mutex
	handlers map[string]contribpubsub.Handler
}

func newNonPausablePubSub() *nonPausablePubSub {
	return &nonPausablePubSub{handlers: make(map[string]contribpubsub.Handler)}
}

func (p *nonPausablePubSub) Init(context.Context, contribpubsub.Metadata) error {
	return nil
}
func (p *nonPausablePubSub) Features() []contribpubsub.Feature { return nil }
func (p *nonPausablePubSub) Publish(context.Context, *contribpubsub.PublishRequest) error {
	return nil
}

func (p *nonPausablePubSub) Subscribe(_ context.Context, req contribpubsub.SubscribeRequest, handler contribpubsub.Handler) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.handlers[req.Topic] = handler
	return nil
}
func (p *nonPausablePubSub) Close() error { return nil }
func (p *nonPausablePubSub) GetComponentMetadata() (map[string]string, []string, []string) {
	return nil, nil, nil
}

func newSubscriptionForTest(t *testing.T, comp contribpubsub.PubSub) *Subscription {
	t.Helper()

	resp := contribpubsub.AppResponse{Status: contribpubsub.Success}
	respB, err := json.Marshal(resp)
	require.NoError(t, err)

	fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
		WithRawDataBytes(respB).
		WithContentType("application/json")
	t.Cleanup(func() { fakeResp.Close() })

	mockAppChannel := new(channelt.MockAppChannel)
	mockAppChannel.Init()
	mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), mock.Anything).Return(fakeResp, nil)

	require.NoError(t, comp.Init(t.Context(), contribpubsub.Metadata{}))

	sub, err := New(Options{
		Resiliency: resiliency.New(log),
		Postman: http.New(http.Options{
			Channels: new(channels.Channels).WithAppChannel(mockAppChannel),
		}),
		PubSub:     &runtimePubsub.PubsubItem{Component: comp},
		AppID:      TestRuntimeConfigID,
		PubSubName: "testpubsub",
		Topic:      "topic0",
		Route: runtimePubsub.Subscription{
			Rules: []*runtimePubsub.Rule{{Path: "orders"}},
		},
	})
	require.NoError(t, err)
	return sub
}

func TestStopPausesPausableSubscriberOnGracefulShutdown(t *testing.T) {
	comp := newPausablePubSub()
	sub := newSubscriptionForTest(t, comp)

	sub.Stop(contribpubsub.ErrGracefulShutdown)

	assert.Equal(t, int64(1), comp.pauseCalled.Load(),
		"Pause should be called exactly once on graceful shutdown")
	assert.Equal(t, int64(0), comp.resumeCalled.Load())
}

func TestStopDoesNotPauseOnNonGracefulShutdown(t *testing.T) {
	t.Run("no error", func(t *testing.T) {
		comp := newPausablePubSub()
		sub := newSubscriptionForTest(t, comp)
		sub.Stop()
		assert.Equal(t, int64(0), comp.pauseCalled.Load(),
			"Pause should not be called when Stop has no cause")
	})

	t.Run("non-graceful error", func(t *testing.T) {
		comp := newPausablePubSub()
		sub := newSubscriptionForTest(t, comp)
		sub.Stop(errors.New("some other error"))
		assert.Equal(t, int64(0), comp.pauseCalled.Load(),
			"Pause should not be called for non-graceful errors")
	})
}

// TestStopCallsPauseBeforeClosing — Pause must run before Stop returns.
func TestStopCallsPauseBeforeClosing(t *testing.T) {
	comp := newPausablePubSub()
	// Block Pause briefly so we can observe Stop's progress.
	comp.pauseGate = make(chan struct{})

	sub := newSubscriptionForTest(t, comp)

	stopReturned := make(chan struct{})
	go func() {
		sub.Stop(contribpubsub.ErrGracefulShutdown)
		close(stopReturned)
	}()

	// Wait until Pause has been entered.
	select {
	case <-comp.pauseStarted:
	case <-time.After(time.Second * 5):
		t.Fatal("timeout waiting for Pause to be called")
	}

	// Stop must not have returned yet — it's blocked inside Pause.
	select {
	case <-stopReturned:
		t.Fatal("Stop returned before Pause completed")
	case <-time.After(time.Millisecond * 100):
	}

	// Release Pause; Stop will proceed.
	close(comp.pauseGate)

	select {
	case <-stopReturned:
	case <-time.After(time.Second * 5):
		t.Fatal("timeout waiting for Stop to return")
	}

	assert.Equal(t, int64(1), comp.pauseCalled.Load(),
		"Pause should have been called exactly once")
	assert.True(t, sub.closed.Load(),
		"closed must be true after Stop returns")
}

// TestStopDrainHoldsForBufferedDelivery — stable-quiet keeps drain open
// long enough for a delivery arriving shortly after Pause to reach the app.
func TestStopDrainHoldsForBufferedDelivery(t *testing.T) {
	comp := newPausablePubSub()
	sub := newSubscriptionForTest(t, comp)

	stopDone := make(chan struct{})
	go func() {
		sub.Stop(contribpubsub.ErrGracefulShutdown)
		close(stopDone)
	}()

	select {
	case <-comp.pauseStarted:
	case <-time.After(time.Second * 5):
		t.Fatal("timeout waiting for Pause to be called")
	}

	// Inject a message ~30ms after Pause returns. The stable-quiet drain
	// requires 100ms of consecutive zero-readings before sealing, so this
	// delivery must land while drainSealed is still false.
	time.Sleep(30 * time.Millisecond)

	deliverCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, comp.deliver(deliverCtx, "topic0", []byte(`{"data":"x"}`)),
		"message arriving shortly after Pause must be delivered to the app, not rejected by drainSealed")

	select {
	case <-stopDone:
	case <-time.After(time.Second * 5):
		t.Fatal("Stop did not return")
	}
}

// TestStopRejectsDeliveryAfterDrainSealed — post-seal deliveries take the
// drainSealed fast path (block on ctx.Done) instead of invoking the app.
func TestStopRejectsDeliveryAfterDrainSealed(t *testing.T) {
	comp := newPausablePubSub()
	sub := newSubscriptionForTest(t, comp)

	sub.Stop(contribpubsub.ErrGracefulShutdown)
	require.True(t, sub.drainSealed.Load(), "drainSealed must be set after Stop returns")

	deliverCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := comp.deliver(deliverCtx, "topic0", []byte(`{"data":"x"}`))
	require.Error(t, err, "delivery after drain seal must be rejected")
	require.ErrorIs(t, err, context.DeadlineExceeded,
		"handler should block on ctx.Done after drainSealed; the test ctx deadline must be the cause")
}

// TestStopBoundsHungPauseWithTimeout — a Pause that ignores ctx must
// not block Stop past the 5s timeout.
func TestStopBoundsHungPauseWithTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 5s timeout test in -short mode")
	}

	comp := newPausablePubSub()
	comp.pauseGate = make(chan struct{}) // never closed; Pause hangs until ctx.Done
	sub := newSubscriptionForTest(t, comp)

	stopDone := make(chan struct{})
	start := time.Now()
	go func() {
		sub.Stop(contribpubsub.ErrGracefulShutdown)
		close(stopDone)
	}()

	select {
	case <-stopDone:
	case <-time.After(time.Second * 7):
		t.Fatal("Stop hung — Pause timeout should have fired within 5s")
	}

	elapsed := time.Since(start)
	require.GreaterOrEqual(t, elapsed, 4*time.Second,
		"Pause should run for several seconds before timing out")
	require.Less(t, elapsed, 7*time.Second,
		"Stop should return shortly after Pause timeout fires")
}

// TestStopOnNonPausableComponentClosesPromptly — close-first path must
// exit on first inflight=0 (no stable-quiet wait).
func TestStopOnNonPausableComponentClosesPromptly(t *testing.T) {
	comp := newNonPausablePubSub()
	sub := newSubscriptionForTest(t, comp)

	_, ok := any(comp).(contribpubsub.PausableSubscriber)
	require.False(t, ok, "test mock must not implement PausableSubscriber")

	start := time.Now()
	sub.Stop(contribpubsub.ErrGracefulShutdown)
	elapsed := time.Since(start)

	// Close-first should exit on first inflight=0 (~one poll interval),
	// well below the 100ms stable-quiet window.
	require.Less(t, elapsed, 80*time.Millisecond,
		"non-pausable Stop should close promptly without 100ms stable-quiet wait")
	require.True(t, sub.closed.Load(), "closed must be true after Stop returns")
	require.True(t, sub.drainSealed.Load(), "drainSealed must be true after Stop returns")
}

// TestStopDrainCeilingForcesSealOnStuckInflight — drainMaxDuration caps
// the worst case where a stuck handler holds both wg and inflight. Stop
// must still return via force-cancel + bounded wg.Wait.
func TestStopDrainCeilingForcesSealOnStuckInflight(t *testing.T) {
	prev := drainMaxDuration
	drainMaxDuration = 200 * time.Millisecond
	t.Cleanup(func() { drainMaxDuration = prev })

	comp := newPausablePubSub()
	sub := newSubscriptionForTest(t, comp)

	// Simulate a stuck handler holding inflight at 1 forever.
	sub.inflight.Add(1)
	t.Cleanup(func() { sub.inflight.Add(-1) })

	stopDone := make(chan struct{})
	start := time.Now()
	go func() {
		sub.Stop(contribpubsub.ErrGracefulShutdown)
		close(stopDone)
	}()

	select {
	case <-stopDone:
	case <-time.After(time.Second * 2):
		t.Fatal("Stop did not honor drainMaxDuration; would block StopAllSubscriptionsForever")
	}

	elapsed := time.Since(start)
	require.GreaterOrEqual(t, elapsed, drainMaxDuration,
		"Stop should drain for at least drainMaxDuration before sealing")
	require.Less(t, elapsed, time.Second,
		"Stop should seal shortly after drainMaxDuration; not run forever")
	require.True(t, sub.drainSealed.Load(),
		"drainSealed must be set even when inflight never stabilized")
}
