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
	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/actors/api"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
)

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
}
