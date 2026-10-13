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

package app

import (
	"context"
	"maps"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/dapr/dapr/pkg/actors/api"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	placementfake "github.com/dapr/dapr/pkg/actors/internal/placement/fake"
	"github.com/dapr/dapr/pkg/actors/internal/reentrancystore"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/actors/targets/app/transport"
	"github.com/dapr/dapr/pkg/channel/fake"
	"github.com/dapr/dapr/pkg/config"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/kit/logger"
)

type callback struct {
	call string
	ids  []string
}

type callLog struct {
	lock  sync.Mutex
	calls []callback
}

func (c *callLog) take() []callback {
	c.lock.Lock()
	defer c.lock.Unlock()
	calls := c.calls
	c.calls = nil
	return calls
}

func newActivationFactory(t *testing.T, opts ...func(*Options)) (targets.Factory, *callLog) {
	t.Helper()

	calls := new(callLog)
	o := Options{
		ActorType:   "abc",
		Reentrancy:  reentrancystore.New(),
		Resiliency:  resiliency.New(logger.NewLogger("test")),
		Placement:   placementfake.New(),
		IdleTimeout: time.Minute,
		AppChannel: fake.New().WithInvokeMethod(func(_ context.Context, req *invokev1.InvokeMethodRequest, _ string) (*invokev1.InvokeMethodResponse, error) {
			calls.lock.Lock()
			calls.calls = append(calls.calls, callback{
				call: req.Message().GetHttpExtension().GetVerb().String() + " " + req.Message().GetMethod(),
				ids:  req.Metadata()[transport.ActivationIDHeader].GetValues(),
			})
			calls.lock.Unlock()
			return invokev1.NewInvokeMethodResponse(http.StatusOK, "", nil), nil
		}),
	}
	for _, opt := range opts {
		opt(&o)
	}

	return New(o), calls
}

func invokeFoo(t *testing.T, target targets.Interface) {
	t.Helper()
	_, err := target.InvokeMethod(t.Context(), internalv1pb.NewInternalInvokeRequest("foo").WithActor("abc", target.ID()))
	require.NoError(t, err)
}

func activationID(t *testing.T, calls *callLog) string {
	t.Helper()
	got := calls.take()
	require.Len(t, got, 1)
	require.Len(t, got[0].ids, 1)
	require.NotEmpty(t, got[0].ids[0])
	return got[0].ids[0]
}

func Test_ActivationIDOnEveryCallback(t *testing.T) {
	t.Parallel()

	fact, calls := newActivationFactory(t)
	target := fact.GetOrCreate("a")
	reminder := &api.Reminder{ActorType: "abc", ActorID: "a", Name: "r"}

	invokeFoo(t, target)
	require.NoError(t, target.InvokeReminder(t.Context(), reminder))
	require.NoError(t, target.InvokeTimer(t.Context(), reminder))
	invokeFoo(t, target)

	got := calls.take()
	require.Len(t, got, 4)
	assert.Equal(t, "PUT actors/abc/a/method/foo", got[0].call)
	assert.Equal(t, "PUT actors/abc/a/method/remind/r", got[1].call)
	assert.Equal(t, "PUT actors/abc/a/method/timer/r", got[2].call)
	assert.Equal(t, "PUT actors/abc/a/method/foo", got[3].call)
	require.Len(t, got[0].ids, 1)
	require.NotEmpty(t, got[0].ids[0])
	for _, c := range got[1:] {
		assert.Equal(t, got[0].ids, c.ids, c.call)
	}

	invokeFoo(t, fact.GetOrCreate("b"))
	assert.NotEqual(t, got[0].ids[0], activationID(t, calls))
}

func Test_ActivationIDChangesPerActivation(t *testing.T) {
	t.Parallel()

	tests := map[string]func(t *testing.T, fact targets.Factory){
		"deactivate": func(t *testing.T, fact targets.Factory) {
			require.NoError(t, fact.GetOrCreate("a").Deactivate(t.Context()))
		},
		"halt all": func(t *testing.T, fact targets.Factory) {
			require.NoError(t, fact.HaltAll(t.Context()))
		},
		"halt non hosted": func(t *testing.T, fact targets.Factory) {
			require.NoError(t, fact.HaltNonHosted(t.Context(), func(*api.LookupActorRequest) bool { return false }))
		},
	}

	for name, deactivate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fact, calls := newActivationFactory(t)
			invokeFoo(t, fact.GetOrCreate("a"))
			first := activationID(t, calls)

			deactivate(t, fact)
			got := calls.take()
			require.Len(t, got, 1)
			assert.Equal(t, "DELETE actors/abc/a", got[0].call)

			invokeFoo(t, fact.GetOrCreate("a"))
			assert.NotEqual(t, first, activationID(t, calls))
		})
	}

	t.Run("idle", func(t *testing.T) {
		t.Parallel()

		clock := clocktesting.NewFakeClock(time.Now())
		fact, calls := newActivationFactory(t, func(o *Options) {
			o.clock = clock
			o.IdleTimeout = 10 * time.Second
		})

		invokeFoo(t, fact.GetOrCreate("a"))
		first := activationID(t, calls)

		require.Eventually(t, func() bool {
			clock.Step(10 * time.Second)
			return !fact.Exists("a")
		}, 5*time.Second, 10*time.Millisecond)
		calls.take()

		invokeFoo(t, fact.GetOrCreate("a"))
		assert.NotEqual(t, first, activationID(t, calls))
	})

	t.Run("new factory", func(t *testing.T) {
		t.Parallel()

		fact, calls := newActivationFactory(t)
		invokeFoo(t, fact.GetOrCreate("a"))
		first := activationID(t, calls)

		next, nextCalls := newActivationFactory(t)
		invokeFoo(t, next.GetOrCreate("a"))
		assert.NotEqual(t, first, activationID(t, nextCalls))
	})
}

func Test_ActivationIDOverridesCaller(t *testing.T) {
	t.Parallel()

	fact, calls := newActivationFactory(t)
	target := fact.GetOrCreate("a")

	invokeFoo(t, target)
	want := activationID(t, calls)

	req := internalv1pb.NewInternalInvokeRequest("foo").WithActor("abc", "a").WithMetadata(map[string][]string{
		"dapr-actor-activation-id": {"spoofed"},
		"DAPR-ACTOR-ACTIVATION-ID": {"spoofed"},
		"other":                    {"kept"},
	})
	_, err := target.InvokeMethod(t.Context(), req)
	require.NoError(t, err)
	assert.Equal(t, want, activationID(t, calls))
	assert.Equal(t, []string{"kept"}, req.GetMetadata()["other"].GetValues())
	assert.Equal(t, []string{"other"}, slices.Collect(maps.Keys(req.GetMetadata())))
}

func Test_ActivationIDReentrant(t *testing.T) {
	t.Parallel()

	store := reentrancystore.New()
	store.Store("abc", config.ReentrancyConfig{Enabled: true})
	fact, calls := newActivationFactory(t, func(o *Options) { o.Reentrancy = store })
	target := fact.GetOrCreate("a")

	invokeFoo(t, target)
	want := activationID(t, calls)

	req := internalv1pb.NewInternalInvokeRequest("foo").WithActor("abc", "a").WithMetadata(map[string][]string{
		"Dapr-Reentrancy-Id": {"r1"},
	})
	_, err := target.InvokeMethod(t.Context(), req)
	require.NoError(t, err)
	assert.Equal(t, want, activationID(t, calls))
	assert.Equal(t, []string{"r1"}, req.GetMetadata()["Dapr-Reentrancy-Id"].GetValues())
	assert.NotContains(t, req.GetMetadata(), transport.ActivationIDHeader)
}

func newTimerTestActor(t *testing.T, fn func(ctx context.Context, method string)) *app {
	t.Helper()
	fact := New(Options{
		ActorType:   "abc",
		Reentrancy:  reentrancystore.New(),
		IdleTimeout: time.Hour,
		Resiliency:  resiliency.New(logger.NewLogger("test")),
		AppChannel: fake.New().WithInvokeMethod(func(ctx context.Context, req *invokev1.InvokeMethodRequest, _ string) (*invokev1.InvokeMethodResponse, error) {
			fn(ctx, req.Message().GetMethod())
			return invokev1.NewInvokeMethodResponse(http.StatusOK, "", nil), nil
		}),
	})
	return fact.GetOrCreate("foo").(*app)
}

func newRemovableTimer() (*api.Reminder, context.CancelFunc) {
	removed, remove := context.WithCancel(context.Background())
	return &api.Reminder{ActorType: "abc", ActorID: "foo", Name: "tick", IsTimer: true, Removed: removed}, remove
}

func TestInvokeTimerAlreadyRemovedDropped(t *testing.T) {
	t.Parallel()

	var timerCalls atomic.Int64
	a := newTimerTestActor(t, func(context.Context, string) { timerCalls.Add(1) })

	timer, remove := newRemovableTimer()
	remove()
	require.ErrorIs(t, a.InvokeTimer(t.Context(), timer), actorerrors.ErrTimerFireStale)
	assert.Zero(t, timerCalls.Load())
}

func TestInvokeTimerRemovedDuringCallbackCompletes(t *testing.T) {
	t.Parallel()

	timer, remove := newRemovableTimer()
	var called, alive atomic.Bool
	a := newTimerTestActor(t, func(ctx context.Context, _ string) {
		called.Store(true)
		remove()
		alive.Store(ctx.Err() == nil)
	})

	require.NoError(t, a.InvokeTimer(t.Context(), timer))
	assert.True(t, called.Load())
	assert.True(t, alive.Load(), "removing the timer cancelled its running callback")
}
