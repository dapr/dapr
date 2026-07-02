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

package inmemory

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/actors/api"
	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
)

func newTimer(t *testing.T, id, period string, at time.Time) *api.Reminder {
	t.Helper()
	p, err := api.NewReminderPeriod(period) // "" => one-shot
	require.NoError(t, err)
	return &api.Reminder{
		ActorType:      "EligibilityShardActor",
		ActorID:        id,
		Name:           "tick",
		Callback:       "OnTick",
		Period:         p,
		RegisteredTime: at,
	}
}

// TestTimerCallbacksDoNotBlockAcrossActors is the regression test for the
// scalability defect: a slow timer callback for one actor must not block the
// timer callback of a different actor. Before the fix all callbacks ran on the
// single processing-loop goroutine, so this failed.
func TestTimerCallbacksDoNotBlockAcrossActors(t *testing.T) {
	slowStarted := make(chan struct{})
	releaseSlow := make(chan struct{})
	fastFired := make(chan struct{})

	router := routerfake.New().WithCallReminderFn(
		func(ctx context.Context, r *api.Reminder) error {
			switch r.ActorID {
			case "slow":
				close(slowStarted)
				<-releaseSlow // hold this callback "in flight"
			case "fast":
				close(fastFired)
			}
			return nil
		},
	)

	store := New(Options{Router: router})
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	now := clock.RealClock{}.Now()
	ctx := context.Background()
	require.NoError(t, store.Create(ctx, newTimer(t, "slow", "", now)))
	require.NoError(t, store.Create(ctx, newTimer(t, "fast", "", now.Add(5*time.Millisecond))))

	select {
	case <-slowStarted:
	case <-time.After(2 * time.Second):
		close(releaseSlow)
		t.Fatal("slow timer never fired")
	}

	select {
	case <-fastFired:
		close(releaseSlow) // cross-actor concurrency observed
	case <-time.After(time.Second):
		close(releaseSlow)
		t.Fatal(`actor "fast" timer was blocked by actor "slow" timer callback`)
	}
}

// TestManyActorsRunConcurrently asserts that timer callbacks for many different
// actors all execute simultaneously: concurrency is unbounded (one loop per
// actor), so N slow callbacks are all in flight at once. A bounded executor
// would cap in-flight callbacks below N and this would time out.
func TestManyActorsRunConcurrently(t *testing.T) {
	const n = 50
	var inFlight atomic.Int32
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }

	router := routerfake.New().WithCallReminderFn(
		func(ctx context.Context, r *api.Reminder) error {
			inFlight.Add(1)
			<-release
			return nil
		},
	)

	store := New(Options{Router: router})
	t.Cleanup(func() {
		releaseAll()
		require.NoError(t, store.Close())
	})

	now := clock.RealClock{}.Now()
	ctx := context.Background()
	for k := range n {
		require.NoError(t, store.Create(ctx, newTimer(t, fmt.Sprintf("actor-%d", k), "", now)))
	}

	require.Eventually(t, func() bool { return inFlight.Load() == int32(n) },
		10*time.Second, 10*time.Millisecond, "all %d actor callbacks should run concurrently", n)
	releaseAll()
}

// TestSameActorTimersRunSeriallyInOrder asserts that multiple timers on the SAME
// actor execute one at a time, in scheduled order, on that actor's loop — the
// intra-actor ordering guarantee. The fake router has no per-actor lock, so the
// serialization observed here comes solely from the per-actor loop.
func TestSameActorTimersRunSeriallyInOrder(t *testing.T) {
	var mu sync.Mutex
	var order []string
	var t1Running, overlap atomic.Bool
	fired := make(chan struct{}, 2)

	router := routerfake.New().WithCallReminderFn(
		func(ctx context.Context, r *api.Reminder) error {
			mu.Lock()
			order = append(order, r.Name)
			mu.Unlock()
			switch r.Name {
			case "t1":
				t1Running.Store(true)
				time.Sleep(40 * time.Millisecond)
				t1Running.Store(false)
			case "t2":
				if t1Running.Load() {
					overlap.Store(true) // t2 ran while t1 was still in flight
				}
			}
			fired <- struct{}{}
			return nil
		},
	)

	store := New(Options{Router: router})
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	now := clock.RealClock{}.Now()
	ctx := context.Background()
	mk := func(name string, at time.Time) *api.Reminder {
		return &api.Reminder{
			ActorType:      "abc",
			ActorID:        "x", // same actor => same loop
			Name:           name,
			Period:         api.NewEmptyReminderPeriod(),
			RegisteredTime: at,
		}
	}
	require.NoError(t, store.Create(ctx, mk("t1", now)))
	require.NoError(t, store.Create(ctx, mk("t2", now.Add(5*time.Millisecond))))

	for range 2 {
		select {
		case <-fired:
		case <-time.After(5 * time.Second):
			t.Fatal("same-actor timers did not both fire")
		}
	}

	assert.False(t, overlap.Load(), "same-actor timer callbacks overlapped")
	mu.Lock()
	assert.Equal(t, []string{"t1", "t2"}, order, "same-actor timers fired out of scheduled order")
	mu.Unlock()
}

// TestRepeatingTimerDoesNotOverlapItself asserts a single repeating timer never
// has two callbacks in flight simultaneously (dapr/dapr#1026): the next tick is
// only armed after the current callback returns.
func TestRepeatingTimerDoesNotOverlapItself(t *testing.T) {
	var inFlight, maxSeen, fires atomic.Int32

	router := routerfake.New().WithCallReminderFn(
		func(ctx context.Context, r *api.Reminder) error {
			fires.Add(1)
			n := inFlight.Add(1)
			for {
				m := maxSeen.Load()
				if n <= m || maxSeen.CompareAndSwap(m, n) {
					break
				}
			}
			time.Sleep(15 * time.Millisecond)
			inFlight.Add(-1)
			return nil
		},
	)

	store := New(Options{Router: router})
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	now := clock.RealClock{}.Now()
	require.NoError(t, store.Create(context.Background(), newTimer(t, "x", "5ms", now)))

	require.Eventually(t, func() bool { return fires.Load() >= 3 }, 2*time.Second, 5*time.Millisecond)
	assert.Equal(t, int32(1), maxSeen.Load(), "a repeating timer overlapped its own callback")
}

// TestTimerDeletedDuringFireIsNotResurrected asserts that deleting a timer while
// its callback is in flight prevents the next tick from being re-armed (the
// guarded re-enqueue), so a deleted repeating timer stops firing.
func TestTimerDeletedDuringFireIsNotResurrected(t *testing.T) {
	firstStarted := make(chan struct{})
	release := make(chan struct{})
	var fires atomic.Int32
	var once atomic.Bool

	router := routerfake.New().WithCallReminderFn(
		func(ctx context.Context, r *api.Reminder) error {
			fires.Add(1)
			if once.CompareAndSwap(false, true) {
				close(firstStarted)
				<-release // hold the first fire in flight
			}
			return nil
		},
	)

	store := New(Options{Router: router})
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	now := clock.RealClock{}.Now()
	tmr := newTimer(t, "x", "10ms", now)
	require.NoError(t, store.Create(context.Background(), tmr))

	<-firstStarted
	store.Delete(context.Background(), tmr.Key()) // delete while the callback is mid-flight
	close(release)

	// The timer must not fire again after deletion.
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int32(1), fires.Load(), "deleted timer was resurrected by the post-callback re-enqueue")
}

// TestCloseCancelsInflightCallback asserts Close aborts a running callback (via
// its context) and returns promptly, rather than hanging until the callback
// finishes on its own.
func TestCloseCancelsInflightCallback(t *testing.T) {
	started := make(chan struct{})
	var sawCancel atomic.Bool

	router := routerfake.New().WithCallReminderFn(
		func(ctx context.Context, r *api.Reminder) error {
			close(started)
			<-ctx.Done() // block until the store cancels us
			sawCancel.Store(ctx.Err() != nil)
			return ctx.Err()
		},
	)

	store := New(Options{Router: router})
	require.NoError(t, store.Create(context.Background(), newTimer(t, "x", "", clock.RealClock{}.Now())))

	<-started
	done := make(chan error, 1)
	go func() { done <- store.Close() }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return: in-flight callback was not cancelled/drained")
	}
	assert.True(t, sawCancel.Load(), "callback context was not cancelled on Close")
}

// TestConcurrentCreateDeleteDoesNotResurrect hammers Create/Delete for the same
// keys while their callbacks fire concurrently, exercising the queueLock that
// makes the activeTimers<->processor transition atomic. It must be race-clean
// (go test -race), and once every timer is deleted it must go quiet — a
// non-atomic re-enqueue could resurrect a just-deleted timer.
func TestConcurrentCreateDeleteDoesNotResurrect(t *testing.T) {
	var fires atomic.Int64
	store := New(Options{Router: routerfake.New().WithCallReminderFn(
		func(context.Context, *api.Reminder) error { fires.Add(1); return nil },
	)})
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	period, err := api.NewReminderPeriod("2ms")
	require.NoError(t, err)
	now := clock.RealClock{}.Now()
	ctx := context.Background()
	keys := []string{"a", "b", "c", "d", "e"}

	mk := func(id string) *api.Reminder {
		return &api.Reminder{
			ActorType:      "EligibilityShardActor",
			ActorID:        id,
			Name:           "tick",
			Callback:       "OnTick",
			Period:         period, // repeating
			RegisteredTime: now,
		}
	}

	var wg sync.WaitGroup
	for _, id := range keys {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			for range 100 {
				_ = store.Create(ctx, mk(id)) // fresh pointer each time
				store.Delete(ctx, mk(id).Key())
			}
		}(id)
	}
	wg.Wait()

	// Everything deleted; after settling, no resurrected timer should keep firing.
	for _, id := range keys {
		store.Delete(ctx, mk(id).Key())
	}
	time.Sleep(50 * time.Millisecond)
	before := fires.Load()
	time.Sleep(150 * time.Millisecond)
	assert.Equal(t, before, fires.Load(), "a deleted timer was resurrected and kept firing")
}
