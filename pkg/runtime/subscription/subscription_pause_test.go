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

// pausablePubSub is a mock pubsub that implements both contribpubsub.PubSub
// and contribpubsub.PausableSubscriber, used for testing the runtime's
// pause-and-drain shutdown path.
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
func (p *pausablePubSub) Pause(context.Context) error {
	p.pauseCalled.Add(1)
	p.pauseOnce.Do(func() { close(p.pauseStarted) })
	if p.pauseGate != nil {
		<-p.pauseGate
	}
	return nil
}

// Resume implements contribpubsub.PausableSubscriber.
func (p *pausablePubSub) Resume(context.Context) error {
	p.resumeCalled.Add(1)
	return nil
}

// Compile-time assertion that the mock implements the interface.
var _ contribpubsub.PausableSubscriber = (*pausablePubSub)(nil)

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

// TestStopCallsPauseBeforeClosing verifies that on graceful shutdown with a
// PausableSubscriber, Pause is called before Stop returns. Pause must run
// before s.cancel so the broker stops fetching while the contrib's
// Subscribe goroutine is winding down.
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

