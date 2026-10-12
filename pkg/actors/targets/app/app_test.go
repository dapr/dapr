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
	"errors"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/dapr/dapr/pkg/actors/api"
	placementfake "github.com/dapr/dapr/pkg/actors/internal/placement/fake"
	"github.com/dapr/dapr/pkg/actors/internal/reentrancystore"
	"github.com/dapr/dapr/pkg/actors/targets"
	targetserrors "github.com/dapr/dapr/pkg/actors/targets/errors"
	"github.com/dapr/dapr/pkg/channel/fake"
	"github.com/dapr/dapr/pkg/config"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/kit/logger"
)

const (
	evictCall  = "DELETE actors/abc/a"
	methodCall = "PUT actors/abc/a/method/foo"
)

type callLog struct {
	lock  sync.Mutex
	calls []string
}

func (c *callLog) add(call string) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.calls = append(c.calls, call)
}

func (c *callLog) peek() []string {
	c.lock.Lock()
	defer c.lock.Unlock()
	return slices.Clone(c.calls)
}

func (c *callLog) take() []string {
	c.lock.Lock()
	defer c.lock.Unlock()
	calls := c.calls
	c.calls = nil
	return calls
}

type replyFn func(ctx context.Context, call string) (*invokev1.InvokeMethodResponse, error)

func newEvictFactory(t *testing.T, store *reentrancystore.Store, reply replyFn, opts ...func(*Options)) (targets.Factory, *callLog) {
	t.Helper()

	if store == nil {
		store = reentrancystore.New()
	}

	calls := new(callLog)
	o := Options{
		ActorType:   "abc",
		Reentrancy:  store,
		Resiliency:  resiliency.New(logger.NewLogger("test")),
		Placement:   placementfake.New(),
		IdleTimeout: time.Minute,
		AppChannel: fake.New().WithInvokeMethod(func(ctx context.Context, req *invokev1.InvokeMethodRequest, _ string) (*invokev1.InvokeMethodResponse, error) {
			call := req.Message().GetHttpExtension().GetVerb().String() + " " + req.Message().GetMethod()
			calls.add(call)
			if reply != nil {
				return reply(ctx, call)
			}
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

func Test_EvictBeforeFirstCallback(t *testing.T) {
	reminder := &api.Reminder{ActorType: "abc", ActorID: "a", Name: "r"}

	t.Run("method", func(t *testing.T) {
		fact, calls := newEvictFactory(t, nil, nil)
		target := fact.GetOrCreate("a")

		invokeFoo(t, target)
		assert.Equal(t, []string{evictCall, methodCall}, calls.take())
		invokeFoo(t, target)
		assert.Equal(t, []string{methodCall}, calls.take())
	})

	t.Run("reminder", func(t *testing.T) {
		fact, calls := newEvictFactory(t, nil, nil)
		target := fact.GetOrCreate("a")

		require.NoError(t, target.InvokeReminder(t.Context(), reminder))
		assert.Equal(t, []string{evictCall, "PUT actors/abc/a/method/remind/r"}, calls.take())
		require.NoError(t, target.InvokeReminder(t.Context(), reminder))
		assert.Equal(t, []string{"PUT actors/abc/a/method/remind/r"}, calls.take())
	})

	t.Run("timer", func(t *testing.T) {
		fact, calls := newEvictFactory(t, nil, nil)
		target := fact.GetOrCreate("a")

		require.NoError(t, target.InvokeTimer(t.Context(), reminder))
		assert.Equal(t, []string{evictCall, "PUT actors/abc/a/method/timer/r"}, calls.take())
		require.NoError(t, target.InvokeTimer(t.Context(), reminder))
		assert.Equal(t, []string{"PUT actors/abc/a/method/timer/r"}, calls.take())
	})

	t.Run("mixed", func(t *testing.T) {
		fact, calls := newEvictFactory(t, nil, nil)
		target := fact.GetOrCreate("a")

		require.NoError(t, target.InvokeTimer(t.Context(), reminder))
		require.NoError(t, target.InvokeReminder(t.Context(), reminder))
		invokeFoo(t, target)
		assert.Equal(t, []string{
			evictCall,
			"PUT actors/abc/a/method/timer/r",
			"PUT actors/abc/a/method/remind/r",
			methodCall,
		}, calls.take())
	})
}

func Test_EvictConcurrentFirstCalls(t *testing.T) {
	fact, calls := newEvictFactory(t, nil, nil)
	target := fact.GetOrCreate("a")

	const n = 20
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			_, err := target.InvokeMethod(t.Context(), internalv1pb.NewInternalInvokeRequest("foo").WithActor("abc", "a"))
			assert.NoError(t, err)
		})
	}
	wg.Wait()

	got := calls.take()
	require.Len(t, got, n+1)
	assert.Equal(t, evictCall, got[0])
	for _, call := range got[1:] {
		assert.Equal(t, methodCall, call)
	}
}

func Test_EvictOncePerActivation(t *testing.T) {
	t.Run("deactivate", func(t *testing.T) {
		fact, calls := newEvictFactory(t, nil, nil)

		invokeFoo(t, fact.GetOrCreate("a"))
		invokeFoo(t, fact.GetOrCreate("a"))
		invokeFoo(t, fact.GetOrCreate("a"))
		require.NoError(t, fact.GetOrCreate("a").Deactivate(t.Context()))
		assert.Equal(t, []string{evictCall, methodCall, methodCall, methodCall, evictCall}, calls.take())

		invokeFoo(t, fact.GetOrCreate("a"))
		invokeFoo(t, fact.GetOrCreate("a"))
		assert.Equal(t, []string{evictCall, methodCall, methodCall}, calls.take())

		invokeFoo(t, fact.GetOrCreate("b"))
		assert.Equal(t, []string{"DELETE actors/abc/b", "PUT actors/abc/b/method/foo"}, calls.take())
	})

	t.Run("halt all", func(t *testing.T) {
		fact, calls := newEvictFactory(t, nil, nil)

		invokeFoo(t, fact.GetOrCreate("a"))
		require.NoError(t, fact.HaltAll(t.Context()))
		assert.Equal(t, []string{evictCall, methodCall, evictCall}, calls.take())

		invokeFoo(t, fact.GetOrCreate("a"))
		assert.Equal(t, []string{evictCall, methodCall}, calls.take())
	})

	t.Run("halt non hosted", func(t *testing.T) {
		fact, calls := newEvictFactory(t, nil, nil)

		invokeFoo(t, fact.GetOrCreate("a"))
		require.NoError(t, fact.HaltNonHosted(t.Context(), func(*api.LookupActorRequest) bool { return false }))
		assert.Equal(t, []string{evictCall, methodCall, evictCall}, calls.take())

		invokeFoo(t, fact.GetOrCreate("a"))
		assert.Equal(t, []string{evictCall, methodCall}, calls.take())
	})

	t.Run("idle", func(t *testing.T) {
		clock := clocktesting.NewFakeClock(time.Now())
		fact, calls := newEvictFactory(t, nil, nil, func(o *Options) {
			o.clock = clock
			o.IdleTimeout = 10 * time.Second
		})

		invokeFoo(t, fact.GetOrCreate("a"))
		assert.Equal(t, []string{evictCall, methodCall}, calls.take())

		stepUntil(t, clock, func() bool { return slices.Equal([]string{evictCall}, calls.peek()) })
		assert.False(t, fact.Exists("a"))
		calls.take()

		invokeFoo(t, fact.GetOrCreate("a"))
		invokeFoo(t, fact.GetOrCreate("a"))
		assert.Equal(t, []string{evictCall, methodCall, methodCall}, calls.take())
	})

	t.Run("new factory", func(t *testing.T) {
		fact, calls := newEvictFactory(t, nil, nil)
		invokeFoo(t, fact.GetOrCreate("a"))
		assert.Equal(t, []string{evictCall, methodCall}, calls.take())

		next, nextCalls := newEvictFactory(t, nil, nil)
		invokeFoo(t, next.GetOrCreate("a"))
		assert.Equal(t, []string{evictCall, methodCall}, nextCalls.take())
	})
}

func Test_EvictTimeoutIsDone(t *testing.T) {
	fact, calls := newEvictFactory(t, nil, func(ctx context.Context, call string) (*invokev1.InvokeMethodResponse, error) {
		if call == evictCall {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return invokev1.NewInvokeMethodResponse(http.StatusOK, "", nil), nil
	}, func(o *Options) { o.evictTimeout = 50 * time.Millisecond })
	target := fact.GetOrCreate("a")

	start := time.Now()
	invokeFoo(t, target)
	assert.Less(t, time.Since(start), 5*time.Second)
	assert.Equal(t, []string{evictCall, methodCall}, calls.take())

	invokeFoo(t, target)
	assert.Equal(t, []string{methodCall}, calls.take())
}

func Test_EvictCallerCancelRetried(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	fact, calls := newEvictFactory(t, nil, func(_ context.Context, call string) (*invokev1.InvokeMethodResponse, error) {
		if call == evictCall && ctx.Err() == nil {
			cancel()
			return nil, context.Canceled
		}
		return invokev1.NewInvokeMethodResponse(http.StatusOK, "", nil), nil
	})
	target := fact.GetOrCreate("a")

	_, _ = target.InvokeMethod(ctx, internalv1pb.NewInternalInvokeRequest("foo").WithActor("abc", "a"))
	assert.Equal(t, evictCall, calls.take()[0])

	invokeFoo(t, target)
	assert.Equal(t, []string{evictCall, methodCall}, calls.take())
}

func Test_EvictAnyAppStatusIsDone(t *testing.T) {
	for _, code := range []int32{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError} {
		t.Run(strconv.Itoa(int(code)), func(t *testing.T) {
			fact, calls := newEvictFactory(t, nil, func(_ context.Context, call string) (*invokev1.InvokeMethodResponse, error) {
				if call == evictCall {
					return invokev1.NewInvokeMethodResponse(code, "", nil), nil
				}
				return invokev1.NewInvokeMethodResponse(http.StatusOK, "", nil).
					WithRawDataString("ok"), nil
			})
			target := fact.GetOrCreate("a")

			resp, err := target.InvokeMethod(t.Context(), internalv1pb.NewInternalInvokeRequest("foo").WithActor("abc", "a"))
			require.NoError(t, err)
			assert.Equal(t, "ok", string(resp.GetMessage().GetData().GetValue()))
			assert.Equal(t, []string{evictCall, methodCall}, calls.take())

			invokeFoo(t, target)
			assert.Equal(t, []string{methodCall}, calls.take())
		})
	}
}

func Test_EvictTransportErrorRetried(t *testing.T) {
	var failed bool
	fact, calls := newEvictFactory(t, nil, func(_ context.Context, call string) (*invokev1.InvokeMethodResponse, error) {
		if call == evictCall && !failed {
			failed = true
			return nil, errors.New("connection refused")
		}
		return invokev1.NewInvokeMethodResponse(http.StatusOK, "", nil), nil
	})
	target := fact.GetOrCreate("a")

	invokeFoo(t, target)
	assert.Equal(t, []string{evictCall, methodCall}, calls.take())
	invokeFoo(t, target)
	assert.Equal(t, []string{evictCall, methodCall}, calls.take())
	invokeFoo(t, target)
	assert.Equal(t, []string{methodCall}, calls.take())
}

func reentrancyStore() *reentrancystore.Store {
	store := reentrancystore.New()
	store.Store("abc", config.ReentrancyConfig{Enabled: true})
	return store
}

func reentrant(method string) *internalv1pb.InternalInvokeRequest {
	req := internalv1pb.NewInternalInvokeRequest(method).WithActor("abc", "a")
	req.Metadata = map[string]*internalv1pb.ListStringValue{
		"Dapr-Reentrancy-Id": {Values: []string{"rid"}},
	}
	return req
}

func Test_EvictNotRetriedInReentrantTurn(t *testing.T) {
	var target targets.Interface
	fact, calls := newEvictFactory(t, reentrancyStore(), func(_ context.Context, call string) (*invokev1.InvokeMethodResponse, error) {
		switch call {
		case evictCall:
			return nil, errors.New("connection refused")
		case "PUT actors/abc/a/method/outer":
			if _, err := target.InvokeMethod(t.Context(), reentrant("inner")); err != nil {
				return nil, err
			}
		}
		return invokev1.NewInvokeMethodResponse(http.StatusOK, "", nil), nil
	})
	target = fact.GetOrCreate("a")

	_, err := target.InvokeMethod(t.Context(), reentrant("outer"))
	require.NoError(t, err)
	assert.Equal(t, []string{
		evictCall,
		"PUT actors/abc/a/method/outer",
		"PUT actors/abc/a/method/inner",
	}, calls.take())

	invokeFoo(t, target)
	assert.Equal(t, []string{evictCall, methodCall}, calls.take())
}

func Test_EvictReentrantCallWaitsForEviction(t *testing.T) {
	var evictReturned atomic.Bool
	evictStarted := make(chan struct{})
	releaseEvict := make(chan struct{})
	methodReached := make(chan struct{}, 2)

	fact, calls := newEvictFactory(t, reentrancyStore(), func(_ context.Context, call string) (*invokev1.InvokeMethodResponse, error) {
		switch {
		case call == evictCall && !evictReturned.Load():
			close(evictStarted)
			<-releaseEvict
			evictReturned.Store(true)
		case call != evictCall:
			assert.True(t, evictReturned.Load(), "%s reached the app before the eviction returned", call)
			methodReached <- struct{}{}
		}
		return invokev1.NewInvokeMethodResponse(http.StatusOK, "", nil), nil
	})
	target := fact.GetOrCreate("a")

	var wg sync.WaitGroup
	invoke := func() {
		wg.Go(func() {
			_, err := target.InvokeMethod(t.Context(), reentrant("foo"))
			assert.NoError(t, err)
		})
	}

	invoke()
	<-evictStarted
	invoke()

	select {
	case <-methodReached:
	case <-time.After(200 * time.Millisecond):
	}
	close(releaseEvict)
	wg.Wait()

	assert.Equal(t, []string{evictCall, methodCall, methodCall}, calls.take())
}

func Test_EvictFailureRearmedAtTurnEnd(t *testing.T) {
	var evicts atomic.Int32
	slowReached := make(chan struct{})
	releaseSlow := make(chan struct{})

	fact, calls := newEvictFactory(t, reentrancyStore(), func(_ context.Context, call string) (*invokev1.InvokeMethodResponse, error) {
		switch call {
		case evictCall:
			if evicts.Add(1) == 1 {
				return nil, errors.New("connection refused")
			}
		case "PUT actors/abc/a/method/first":
			<-slowReached
		case "PUT actors/abc/a/method/slow":
			close(slowReached)
			<-releaseSlow
		}
		return invokev1.NewInvokeMethodResponse(http.StatusOK, "", nil), nil
	})
	target := fact.GetOrCreate("a")

	var first, slow sync.WaitGroup
	first.Go(func() {
		_, err := target.InvokeMethod(t.Context(), reentrant("first"))
		assert.NoError(t, err)
	})
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, calls.peek(), 2)
	}, 5*time.Second, time.Millisecond)
	slow.Go(func() {
		_, err := target.InvokeMethod(t.Context(), reentrant("slow"))
		assert.NoError(t, err)
	})
	first.Wait()

	_, err := target.InvokeMethod(t.Context(), reentrant("foo"))
	require.NoError(t, err)
	close(releaseSlow)
	slow.Wait()
	assert.Equal(t, []string{
		evictCall,
		"PUT actors/abc/a/method/first",
		"PUT actors/abc/a/method/slow",
		methodCall,
	}, calls.take())

	invokeFoo(t, target)
	assert.Equal(t, []string{evictCall, methodCall}, calls.take())
}

func waitFor(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-t.Context().Done():
		require.Fail(t, "test context done before signal")
	}
}

func received(ch <-chan struct{}) func() bool {
	return func() bool {
		select {
		case <-ch:
			return true
		default:
			return false
		}
	}
}

func stepUntil(t *testing.T, clock *clocktesting.FakeClock, cond func() bool) {
	t.Helper()
	require.Eventually(t, func() bool {
		clock.Step(10 * time.Second)
		return cond()
	}, 10*time.Second, 10*time.Millisecond)
}

func Test_EvictIdleFiresDuringEvict(t *testing.T) {
	t.Parallel()

	clock := clocktesting.NewFakeClock(time.Now())
	evictStarted := make(chan struct{})
	releaseEvict := make(chan struct{})
	idlerReturned := make(chan struct{}, 2)

	var evicts atomic.Int32
	fact, calls := newEvictFactory(t, nil, func(_ context.Context, call string) (*invokev1.InvokeMethodResponse, error) {
		if call == evictCall && evicts.Add(1) == 1 {
			close(evictStarted)
			<-releaseEvict
		}
		return invokev1.NewInvokeMethodResponse(http.StatusOK, "", nil), nil
	}, func(o *Options) {
		o.clock = clock
		o.IdleTimeout = 10 * time.Second
		o.Placement = placementfake.New().WithLock(func(ctx context.Context, _ string) (context.Context, context.CancelCauseFunc, error) {
			return ctx, func(error) { idlerReturned <- struct{}{} }, nil
		})
	})
	target := fact.GetOrCreate("a")

	errCh := make(chan error, 1)
	go func() {
		_, err := target.InvokeMethod(t.Context(), internalv1pb.NewInternalInvokeRequest("foo").WithActor("abc", "a"))
		errCh <- err
	}()

	waitFor(t, evictStarted)
	stepUntil(t, clock, received(idlerReturned))

	close(releaseEvict)
	require.NoError(t, <-errCh)
	assert.Equal(t, []string{evictCall, methodCall}, calls.take())
	assert.True(t, fact.Exists("a"))

	stepUntil(t, clock, received(idlerReturned))
	assert.False(t, fact.Exists("a"))
	assert.Equal(t, []string{evictCall}, calls.take())
}

func Test_EvictClosedDuringEvict(t *testing.T) {
	t.Parallel()

	invokeMethod := func(ctx context.Context, target targets.Interface) error {
		_, err := target.InvokeMethod(ctx, internalv1pb.NewInternalInvokeRequest("foo").WithActor("abc", "a"))
		return err
	}
	invokeReminder := func(ctx context.Context, target targets.Interface) error {
		return target.InvokeReminder(ctx, &api.Reminder{ActorType: "abc", ActorID: "a", Name: "r"})
	}
	deactivate := func(ctx context.Context, _ targets.Factory, target targets.Interface) error {
		return target.Deactivate(ctx)
	}
	haltAll := func(ctx context.Context, fact targets.Factory, _ targets.Interface) error {
		return fact.HaltAll(ctx)
	}

	tests := map[string]struct {
		invoke    func(context.Context, targets.Interface) error
		close     func(context.Context, targets.Factory, targets.Interface) error
		ignoreCtx bool
	}{
		"method deactivate":             {invoke: invokeMethod, close: deactivate},
		"method halt all":               {invoke: invokeMethod, close: haltAll},
		"reminder deactivate":           {invoke: invokeReminder, close: deactivate},
		"reminder halt all":             {invoke: invokeReminder, close: haltAll},
		"method deactivate ignores ctx": {invoke: invokeMethod, close: deactivate, ignoreCtx: true},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			evictStarted := make(chan struct{})
			releaseEvict := make(chan struct{})

			var evicts atomic.Int32
			fact, calls := newEvictFactory(t, nil, func(ctx context.Context, call string) (*invokev1.InvokeMethodResponse, error) {
				if call == evictCall && evicts.Add(1) == 1 {
					close(evictStarted)
					if test.ignoreCtx {
						<-releaseEvict
						return invokev1.NewInvokeMethodResponse(http.StatusOK, "", nil), nil
					}
					<-ctx.Done()
					return nil, ctx.Err()
				}
				return invokev1.NewInvokeMethodResponse(http.StatusOK, "", nil), nil
			})
			target := fact.GetOrCreate("a")

			errCh := make(chan error, 1)
			go func() { errCh <- test.invoke(t.Context(), target) }()

			waitFor(t, evictStarted)
			require.NoError(t, test.close(t.Context(), fact, target))
			close(releaseEvict)

			err := <-errCh
			assert.True(t, targetserrors.IsClosed(err), "expected closed error, got %v", err)
			assert.Equal(t, []string{evictCall, evictCall}, calls.take())
			assert.False(t, fact.Exists("a"))

			invokeFoo(t, fact.GetOrCreate("a"))
			assert.Equal(t, []string{evictCall, methodCall}, calls.take())
		})
	}
}

func Test_EvictFlagClearedOnPanic(t *testing.T) {
	t.Parallel()

	clock := clocktesting.NewFakeClock(time.Now())
	var evicts atomic.Int32
	fact, calls := newEvictFactory(t, nil, func(_ context.Context, call string) (*invokev1.InvokeMethodResponse, error) {
		if call == evictCall && evicts.Add(1) == 1 {
			panic("transport panic")
		}
		return invokev1.NewInvokeMethodResponse(http.StatusOK, "", nil), nil
	}, func(o *Options) {
		o.clock = clock
		o.IdleTimeout = 10 * time.Second
	})
	target := fact.GetOrCreate("a")

	panicked := make(chan any, 1)
	go func() {
		defer func() { panicked <- recover() }()
		_, _ = target.InvokeMethod(t.Context(), internalv1pb.NewInternalInvokeRequest("foo").WithActor("abc", "a"))
	}()
	require.Equal(t, "transport panic", <-panicked)

	invokeFoo(t, target)
	assert.Equal(t, []string{evictCall, evictCall, methodCall}, calls.take())

	stepUntil(t, clock, func() bool { return !fact.Exists("a") })
}

func Test_EvictHaltAllDoesNotRequeueDeadApp(t *testing.T) {
	t.Parallel()

	clock := clocktesting.NewFakeClock(time.Now())
	evictStarted := make(chan struct{})
	releaseEvict := make(chan struct{})

	var evicts atomic.Int32
	fact, _ := newEvictFactory(t, nil, func(_ context.Context, call string) (*invokev1.InvokeMethodResponse, error) {
		if call == evictCall && evicts.Add(1) == 1 {
			close(evictStarted)
			<-releaseEvict
		}
		return invokev1.NewInvokeMethodResponse(http.StatusOK, "", nil), nil
	}, func(o *Options) {
		o.clock = clock
		o.IdleTimeout = 10 * time.Second
	})
	target := fact.GetOrCreate("a")

	reactivated := make(chan struct{})
	target.(*app).endEvictHook = func() {
		go func() {
			defer close(reactivated)
			assert.NoError(t, fact.HaltAll(t.Context()))
			_, err := fact.GetOrCreate("a").InvokeMethod(t.Context(), internalv1pb.NewInternalInvokeRequest("foo").WithActor("abc", "a"))
			assert.NoError(t, err)
		}()
		select {
		case <-reactivated:
		case <-time.After(200 * time.Millisecond):
		}
	}

	turnDone := make(chan struct{})
	go func() {
		defer close(turnDone)
		_, _ = target.InvokeMethod(t.Context(), internalv1pb.NewInternalInvokeRequest("foo").WithActor("abc", "a"))
	}()
	waitFor(t, evictStarted)

	close(releaseEvict)
	waitFor(t, turnDone)
	waitFor(t, reactivated)
	require.True(t, fact.Exists("a"))

	stepUntil(t, clock, func() bool { return !fact.Exists("a") })
}
