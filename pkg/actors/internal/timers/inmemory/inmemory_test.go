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
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opencensus.io/stats/view"
	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/actors/api"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
	diag "github.com/dapr/dapr/pkg/diagnostics"
)

func newTimerMeter(t *testing.T) view.Meter {
	t.Helper()
	meter := view.NewMeter()
	meter.Start()
	require.NoError(t, diag.DefaultMonitoring.Init(meter, "test", view.Distribution(1)))
	return meter
}

func countMetric(t *testing.T, meter view.Meter, name string, want map[string]string) int64 {
	t.Helper()
	rows, err := meter.RetrieveData(name)
	require.NoError(t, err)
	var n int64
	for _, row := range rows {
		got := make(map[string]string, len(row.Tags))
		for _, tg := range row.Tags {
			got[tg.Key.Name()] = tg.Value
		}
		match := true
		for k, v := range want {
			if got[k] != v {
				match = false
			}
		}
		if match {
			n += row.Data.(*view.CountData).Value
		}
	}
	return n
}

func timersDropped(t *testing.T, meter view.Meter) int64 {
	t.Helper()
	return countMetric(t, meter, "runtime/actor/timers_dropped_total", nil)
}

func timersFailed(t *testing.T, meter view.Meter) int64 {
	t.Helper()
	return countMetric(t, meter, "runtime/actor/timers_fired_total", map[string]string{"success": "false"})
}

// TestUpdateActiveTimersCountConcurrent exercises updateActiveTimersCount from
// many goroutines using distinct actor types. First-seen actor types insert a
// new key into the activeTimersCount map, so this makes concurrent map writes
// overlap the map read used to increment the counter. Run with -race to catch a
// regression of the concurrent map read/write data race.
func TestUpdateActiveTimersCountConcurrent(t *testing.T) {
	i, ok := New(Options{Router: routerfake.New()}).(*inmemory)
	require.True(t, ok)

	const actorTypes = 200
	var wg sync.WaitGroup
	wg.Add(actorTypes)
	for a := range actorTypes {
		go func(a int) {
			defer wg.Done()
			i.updateActiveTimersCount("actor-"+strconv.Itoa(a), 1)
		}(a)
	}
	wg.Wait()

	for a := range actorTypes {
		assert.Equal(t, int64(1), i.GetActiveTimersCount("actor-"+strconv.Itoa(a)))
	}
}

// TestUpdateActiveTimersCountConcurrentSameType checks that concurrent updates
// for a single actor type are counted correctly, guarding the atomic counter
// against lost updates.
func TestUpdateActiveTimersCountConcurrentSameType(t *testing.T) {
	i, ok := New(Options{Router: routerfake.New()}).(*inmemory)
	require.True(t, ok)

	const increments = 500
	var wg sync.WaitGroup
	wg.Add(increments)
	for range increments {
		go func() {
			defer wg.Done()
			i.updateActiveTimersCount("actor-type", 1)
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(increments), i.GetActiveTimersCount("actor-type"))
}

func TestDeleteFuncRemovesMatchingActors(t *testing.T) {
	store := New(Options{Router: routerfake.New()})
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	im, ok := store.(*inmemory)
	require.True(t, ok)

	future := clock.RealClock{}.Now().Add(time.Hour)
	ctx := context.Background()
	moved1 := newNamedTimer(t, "moved", "one", "", future)
	moved2 := newNamedTimer(t, "moved", "two", "", future)
	kept := newNamedTimer(t, "kept", "one", "", future)
	require.NoError(t, store.Create(ctx, moved1))
	require.NoError(t, store.Create(ctx, moved2))
	require.NoError(t, store.Create(ctx, kept))
	require.Equal(t, int64(3), im.GetActiveTimersCount(kept.ActorType))

	store.DeleteFunc(ctx, func(actorType, actorID string) bool {
		assert.Equal(t, kept.ActorType, actorType)
		return actorID == "moved"
	})

	assert.Equal(t, int64(1), im.GetActiveTimersCount(kept.ActorType))
	_, ok = im.activeTimers.Load(moved1.Key())
	assert.False(t, ok)
	_, ok = im.activeTimers.Load(moved2.Key())
	assert.False(t, ok)
	_, ok = im.activeTimers.Load(kept.Key())
	assert.True(t, ok)
	_, _, exists := stateSnapshot(im, moved1.ActorKey())
	assert.False(t, exists, "deleted actor's state was not reaped")
	_, _, exists = stateSnapshot(im, kept.ActorKey())
	assert.True(t, exists)
}

func TestDeleteFuncNoMatch(t *testing.T) {
	store := New(Options{Router: routerfake.New()})
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	im, ok := store.(*inmemory)
	require.True(t, ok)

	future := clock.RealClock{}.Now().Add(time.Hour)
	ctx := context.Background()
	timer := newTimer(t, "abc", "", future)
	require.NoError(t, store.Create(ctx, timer))

	store.DeleteFunc(ctx, func(string, string) bool { return false })

	assert.Equal(t, int64(1), im.GetActiveTimersCount(timer.ActorType))
	_, ok = im.activeTimers.Load(timer.Key())
	assert.True(t, ok)
}

func TestCreateCancelledContext(t *testing.T) {
	store := New(Options{Router: routerfake.New()})
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	im, ok := store.(*inmemory)
	require.True(t, ok)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	timer := newTimer(t, "abc", "", clock.RealClock{}.Now().Add(time.Hour))
	require.ErrorIs(t, store.Create(ctx, timer), context.Canceled)

	_, ok = im.activeTimers.Load(timer.Key())
	assert.False(t, ok)
	assert.Equal(t, int64(0), im.GetActiveTimersCount(timer.ActorType))
}

func TestNotLocalFireDeletesTimer(t *testing.T) {
	var calls atomic.Int64
	router := routerfake.New().WithCallReminderFn(
		func(context.Context, *api.Reminder) error {
			calls.Add(1)
			return backoff.Permanent(actorerrors.ErrTimerFireNotLocal)
		},
	)

	store := New(Options{Router: router})
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	im, ok := store.(*inmemory)
	require.True(t, ok)

	timer := newTimer(t, "abc", "1s", clock.RealClock{}.Now())
	require.NoError(t, store.Create(context.Background(), timer))

	require.Eventually(t, func() bool { return calls.Load() == 1 }, 5*time.Second, time.Millisecond)
	require.Eventually(t, func() bool {
		_, _, exists := stateSnapshot(im, timer.ActorKey())
		return !exists
	}, 5*time.Second, time.Millisecond, "dropped timer's state was not reaped")

	_, ok = im.activeTimers.Load(timer.Key())
	assert.False(t, ok, "dropped timer must be removed, not rescheduled")
	assert.Equal(t, int64(0), im.GetActiveTimersCount(timer.ActorType))
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int64(1), calls.Load(), "dropped timer must not fire again")
}

func TestDeleteFuncSuppressesParkedFire(t *testing.T) {
	slowStarted := make(chan struct{})
	release := make(chan struct{})
	var victimFired atomic.Bool

	router := routerfake.New().WithCallReminderFn(
		func(ctx context.Context, r *api.Reminder) error {
			switch r.Name {
			case "slow":
				close(slowStarted)
				<-release
			case "victim":
				victimFired.Store(true)
			}
			return nil
		},
	)

	store := New(Options{Router: router})
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	im, ok := store.(*inmemory)
	require.True(t, ok)

	meter := newTimerMeter(t)
	now := clock.RealClock{}.Now()
	ctx := context.Background()
	require.NoError(t, store.Create(ctx, newNamedTimer(t, "x", "slow", "", now)))
	<-slowStarted

	victim := newNamedTimer(t, "x", "victim", "", now)
	require.NoError(t, store.Create(ctx, victim))

	require.Eventually(t, func() bool {
		_, pending, _ := stateSnapshot(im, victim.ActorKey())
		return pending == 2
	}, 5*time.Second, time.Millisecond, "victim fire was never routed to the actor loop")

	store.DeleteFunc(ctx, func(_, actorID string) bool { return actorID == "x" })
	close(release)

	require.Eventually(t, func() bool {
		_, _, exists := stateSnapshot(im, victim.ActorKey())
		return !exists
	}, 5*time.Second, time.Millisecond)
	assert.False(t, victimFired.Load(), "a swept parked fire executed its callback")
	assert.Equal(t, int64(1), timersDropped(t, meter), "a swept parked fire was not counted as dropped")
}

func TestListReturnsActorTimersSorted(t *testing.T) {
	store := New(Options{Router: routerfake.New()})
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	im, ok := store.(*inmemory)
	require.True(t, ok)

	future := clock.RealClock{}.Now().Add(time.Hour)
	ctx := context.Background()
	oneB := newNamedTimer(t, "one", "b", "", future)
	oneA := newNamedTimer(t, "one", "a", "10s", future)
	twoA := newNamedTimer(t, "two", "a", "", future)
	require.NoError(t, store.Create(ctx, oneB))
	require.NoError(t, store.Create(ctx, oneA))
	require.NoError(t, store.Create(ctx, twoA))

	got := store.List(ctx, oneA.ActorType, "one")
	require.Len(t, got, 2)
	assert.Equal(t, "a", got[0].Name)
	assert.Equal(t, "b", got[1].Name)
	assert.Equal(t, "10s", got[0].Period.String())
	assert.Equal(t, "OnTick", got[0].Callback)

	// The list hands out copies, never the stored pointers.
	stored, ok := im.activeTimers.Load(oneA.Key())
	require.True(t, ok)
	assert.NotSame(t, stored.(*api.Reminder), got[0])

	assert.Empty(t, store.List(ctx, oneA.ActorType, "unknown"))
	assert.Empty(t, store.List(ctx, "OtherType", "one"))

	store.Delete(ctx, oneA.Key())
	got = store.List(ctx, oneA.ActorType, "one")
	require.Len(t, got, 1)
	assert.Equal(t, "b", got[0].Name)
}

func TestGetReturnsCopyOrNil(t *testing.T) {
	store := New(Options{Router: routerfake.New()})
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	im, ok := store.(*inmemory)
	require.True(t, ok)

	future := clock.RealClock{}.Now().Add(time.Hour)
	ctx := context.Background()
	timer := newNamedTimer(t, "one", "a", "10s", future)
	require.NoError(t, store.Create(ctx, timer))

	got := store.Get(ctx, timer.Key())
	require.NotNil(t, got)
	assert.Equal(t, "a", got.Name)
	assert.Equal(t, "one", got.ActorID)
	assert.Equal(t, "10s", got.Period.String())

	stored, ok := im.activeTimers.Load(timer.Key())
	require.True(t, ok)
	assert.NotSame(t, stored.(*api.Reminder), got)

	assert.Nil(t, store.Get(ctx, newNamedTimer(t, "one", "missing", "", future).Key()))

	store.Delete(ctx, timer.Key())
	assert.Nil(t, store.Get(ctx, timer.Key()))
}

func TestIsRegistered(t *testing.T) {
	t.Parallel()

	store := New(Options{Router: routerfake.New()})
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	future := clock.RealClock{}.Now().Add(time.Hour)
	ctx := context.Background()

	timer := newTimer(t, "x", "", future)
	require.NoError(t, store.Create(ctx, timer))
	assert.True(t, store.IsRegistered(timer))
	assert.False(t, store.IsRegistered(store.Get(ctx, timer.Key())), "a copy is not the registered timer")

	store.Delete(ctx, timer.Key())
	assert.False(t, store.IsRegistered(timer))

	require.NoError(t, store.Create(ctx, timer))
	replacement := newTimer(t, "x", "", future)
	require.NoError(t, store.Create(ctx, replacement))
	assert.False(t, store.IsRegistered(timer))
	assert.True(t, store.IsRegistered(replacement))

	store.DeleteFunc(ctx, func(_, actorID string) bool { return actorID == "x" })
	assert.False(t, store.IsRegistered(replacement))
}

func TestStaleFireOnDeletedTimerIsDropped(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int64
	router := routerfake.New().WithCallReminderFn(
		func(context.Context, *api.Reminder) error {
			calls.Add(1)
			close(started)
			<-release
			return backoff.Permanent(actorerrors.ErrTimerFireStale)
		},
	)

	store := New(Options{Router: router})
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	im, ok := store.(*inmemory)
	require.True(t, ok)

	meter := newTimerMeter(t)
	ctx := context.Background()
	timer := newTimer(t, "abc", "1s", clock.RealClock{}.Now())
	require.NoError(t, store.Create(ctx, timer))
	<-started

	store.Delete(ctx, timer.Key())
	require.Equal(t, int64(0), im.GetActiveTimersCount(timer.ActorType))
	close(release)

	require.Eventually(t, func() bool {
		_, _, exists := stateSnapshot(im, timer.ActorKey())
		return !exists
	}, 5*time.Second, time.Millisecond, "deleted timer's state was not reaped")

	_, ok = im.activeTimers.Load(timer.Key())
	assert.False(t, ok, "stale fire re-enqueued its deleted timer")
	assert.Equal(t, int64(0), im.GetActiveTimersCount(timer.ActorType), "stale fire changed the counts")
	assert.Equal(t, int64(1), calls.Load())
	assert.Equal(t, int64(1), timersDropped(t, meter), "stale fire was not counted as dropped")
	assert.Zero(t, timersFailed(t, meter), "stale fire was counted as a failed fire")
}

func TestStaleFireOnReplacedTimerKeepsReplacement(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int64
	router := routerfake.New().WithCallReminderFn(
		func(context.Context, *api.Reminder) error {
			if calls.Add(1) == 1 {
				close(started)
				<-release
				return backoff.Permanent(actorerrors.ErrTimerFireStale)
			}
			return nil
		},
	)

	store := New(Options{Router: router})
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	im, ok := store.(*inmemory)
	require.True(t, ok)

	meter := newTimerMeter(t)
	ctx := context.Background()
	now := clock.RealClock{}.Now()
	timer := newTimer(t, "x", "1s", now)
	require.NoError(t, store.Create(ctx, timer))
	<-started

	replacement := newTimer(t, "x", "1h", now.Add(time.Hour))
	require.NoError(t, store.Create(ctx, replacement))
	close(release)

	require.Eventually(t, func() bool {
		_, pending, _ := stateSnapshot(im, timer.ActorKey())
		return pending == 0
	}, 5*time.Second, time.Millisecond, "stale fire did not complete")
	cur, ok := im.activeTimers.Load(timer.Key())
	require.True(t, ok, "stale fire removed the replacement")
	assert.Same(t, replacement, cur)
	assert.True(t, store.IsRegistered(replacement))
	assert.Equal(t, int64(1), im.GetActiveTimersCount(timer.ActorType))
	assert.Equal(t, int64(1), calls.Load())
	assert.Equal(t, int64(1), timersDropped(t, meter), "stale fire was not counted as dropped")
	assert.Zero(t, timersFailed(t, meter), "stale fire was counted as a failed fire")
}
