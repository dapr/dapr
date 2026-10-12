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
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
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
	placementfake "github.com/dapr/dapr/pkg/actors/internal/placement/fake"
	"github.com/dapr/dapr/pkg/actors/internal/reentrancystore"
	"github.com/dapr/dapr/pkg/actors/router"
	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
	tablefake "github.com/dapr/dapr/pkg/actors/table/fake"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/actors/targets/app"
	channelfake "github.com/dapr/dapr/pkg/channel/fake"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/kit/logger"
)

// parkedMeter keeps DefaultMonitoring off a stopped meter between tests.
var parkedMeter view.Meter

// newTimerMeter must be called before the store is created.
func newTimerMeter(t *testing.T) view.Meter {
	t.Helper()
	meter := startTimerMeter(t)
	if parkedMeter != nil {
		parkedMeter.Stop()
		parkedMeter = nil
	}
	t.Cleanup(func() {
		parkedMeter = startTimerMeter(t)
		meter.Stop()
	})
	return meter
}

func startTimerMeter(t *testing.T) view.Meter {
	t.Helper()
	meter := view.NewMeter()
	meter.Start()
	require.NoError(t, diag.DefaultMonitoring.Init(meter, "test", view.Distribution(1)))
	return meter
}

func metricRows(t *testing.T, meter view.Meter, name string, want map[string]string) []*view.Row {
	t.Helper()
	rows, err := meter.RetrieveData(name)
	require.NoError(t, err)
	var out []*view.Row
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
			out = append(out, row)
		}
	}
	return out
}

func countMetric(t *testing.T, meter view.Meter, name string, want map[string]string) int64 {
	t.Helper()
	var n int64
	for _, row := range metricRows(t, meter, name, want) {
		n += row.Data.(*view.CountData).Value
	}
	return n
}

func pendingActorCalls(t *testing.T, meter view.Meter, actorType string) float64 {
	t.Helper()
	rows := metricRows(t, meter, "runtime/actor/pending_actor_calls", map[string]string{"actor_type": actorType})
	if len(rows) == 0 {
		return 0
	}
	return rows[0].Data.(*view.LastValueData).Value
}

func timersDropped(t *testing.T, meter view.Meter) int64 {
	t.Helper()
	return countMetric(t, meter, "runtime/actor/timers_dropped_total", nil)
}

func timersFailed(t *testing.T, meter view.Meter) int64 {
	t.Helper()
	return countMetric(t, meter, "runtime/actor/timers_fired_total", map[string]string{"success": "false"})
}

func timersSucceeded(t *testing.T, meter view.Meter) int64 {
	t.Helper()
	return countMetric(t, meter, "runtime/actor/timers_fired_total", map[string]string{"success": "true"})
}

func newAppStore(t *testing.T, actorType string, lookup func(context.Context) error, callback func(context.Context, *invokev1.InvokeMethodRequest)) (*inmemory, targets.Factory) {
	t.Helper()
	log := logger.NewLogger("test")
	fact := app.New(app.Options{
		ActorType:   actorType,
		Reentrancy:  reentrancystore.New(),
		IdleTimeout: time.Hour,
		Resiliency:  resiliency.New(log),
		AppChannel: channelfake.New().WithInvokeMethod(func(ctx context.Context, req *invokev1.InvokeMethodRequest, _ string) (*invokev1.InvokeMethodResponse, error) {
			callback(ctx, req)
			return invokev1.NewInvokeMethodResponse(http.StatusOK, "", nil), nil
		}),
	})
	plc := placementfake.New().WithLookupActor(func(ctx context.Context, _ *api.LookupActorRequest) (*api.LookupActorResponse, context.Context, context.CancelCauseFunc, error) {
		if lookup != nil {
			if err := lookup(ctx); err != nil {
				return nil, nil, nil, err
			}
		}
		return &api.LookupActorResponse{Local: true}, ctx, func(error) {}, nil
	})
	rt := router.New(router.Options{
		Placement: plc,
		Table: tablefake.New().WithGetOrCreate(func(_ context.Context, _, actorID string) (targets.Interface, error) {
			return fact.GetOrCreate(actorID), nil
		}),
		Resiliency: resiliency.FromConfigurations(log),
	})
	store, ok := New(Options{Router: rt}).(*inmemory)
	require.True(t, ok)
	return store, fact
}

func appTimer(t *testing.T, actorType, name, period string, at time.Time) *api.Reminder {
	t.Helper()
	timer := newNamedTimer(t, "x", name, period, at)
	timer.ActorType = actorType
	timer.IsTimer = true
	return timer
}

func timerCallback(t *testing.T, req *invokev1.InvokeMethodRequest) string {
	t.Helper()
	raw, err := req.RawDataFull()
	assert.NoError(t, err)
	var body struct {
		Callback string `json:"callback"`
	}
	assert.NoError(t, json.Unmarshal(raw, &body))
	return body.Callback
}

func waitReaped(t *testing.T, store *inmemory, timer *api.Reminder) {
	t.Helper()
	require.Eventually(t, func() bool {
		_, _, exists := stateSnapshot(store, timer.ActorKey())
		return !exists
	}, 5*time.Second, time.Millisecond, "the timer's actor state was not reaped")
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

	meter := newTimerMeter(t)
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

func TestDeleteWhileFireWaitsOnActorLock(t *testing.T) {
	meter := newTimerMeter(t)
	holding := make(chan struct{})
	release := make(chan struct{})
	var fired atomic.Int64
	store, fact := newAppStore(t, "lockwait", nil, func(_ context.Context, req *invokev1.InvokeMethodRequest) {
		if strings.HasSuffix(req.Message().GetMethod(), "/method/hold") {
			close(holding)
			<-release
			return
		}
		fired.Add(1)
	})
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	ctx := context.Background()
	methodErr := make(chan error, 1)
	go func() {
		_, err := fact.GetOrCreate("x").InvokeMethod(ctx, internalv1pb.NewInternalInvokeRequest("hold").WithActor("lockwait", "x"))
		methodErr <- err
	}()
	<-holding

	timer := appTimer(t, "lockwait", "tick", "1s", clock.RealClock{}.Now())
	require.NoError(t, store.Create(ctx, timer))
	require.Eventually(t, func() bool {
		return pendingActorCalls(t, meter, "lockwait") == 1
	}, 5*time.Second, time.Millisecond, "the timer fire never waited on the actor lock")

	store.Delete(ctx, timer.Key())
	close(release)
	require.NoError(t, <-methodErr)
	waitReaped(t, store, timer)

	assert.Zero(t, fired.Load(), "a timer deleted while its fire waited on the actor lock still fired")
	assert.Equal(t, int64(1), timersDropped(t, meter))
	assert.Zero(t, timersFailed(t, meter), "a dropped fire was counted as failed")
}

func TestDeleteWhileFireWaitsInLookup(t *testing.T) {
	meter := newTimerMeter(t)
	waiting := make(chan struct{})
	var fired atomic.Int64
	store, _ := newAppStore(t, "lookupwait", func(ctx context.Context) error {
		close(waiting)
		<-ctx.Done()
		return ctx.Err()
	}, func(context.Context, *invokev1.InvokeMethodRequest) { fired.Add(1) })
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	ctx := context.Background()
	timer := appTimer(t, "lookupwait", "tick", "1s", clock.RealClock{}.Now())
	require.NoError(t, store.Create(ctx, timer))
	<-waiting

	store.Delete(ctx, timer.Key())
	waitReaped(t, store, timer)

	assert.Zero(t, fired.Load())
	assert.Equal(t, int64(1), timersDropped(t, meter))
	assert.Zero(t, timersFailed(t, meter), "a dropped fire was counted as failed")
}

func TestCloseDuringLookupIsNotDropped(t *testing.T) {
	meter := newTimerMeter(t)
	waiting := make(chan struct{})
	store, _ := newAppStore(t, "closewait", func(ctx context.Context) error {
		close(waiting)
		<-ctx.Done()
		return ctx.Err()
	}, func(context.Context, *invokev1.InvokeMethodRequest) {})

	require.NoError(t, store.Create(context.Background(), appTimer(t, "closewait", "tick", "", clock.RealClock{}.Now())))
	<-waiting
	require.NoError(t, store.Close())

	assert.Zero(t, timersDropped(t, meter), "a fire cut off by Close was counted as dropped")
	assert.Equal(t, int64(1), timersFailed(t, meter))
}

func TestReplaceCancelsOnlyTheOldFire(t *testing.T) {
	t.Parallel()

	waiting := make(chan struct{})
	var lookups atomic.Int64
	var mu sync.Mutex
	var fired []string
	store, _ := newAppStore(t, "replace", func(ctx context.Context) error {
		if lookups.Add(1) == 1 {
			close(waiting)
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}, func(_ context.Context, req *invokev1.InvokeMethodRequest) {
		mu.Lock()
		defer mu.Unlock()
		fired = append(fired, timerCallback(t, req))
	})
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	ctx := context.Background()
	now := clock.RealClock{}.Now()
	old := appTimer(t, "replace", "tick", "", now)
	old.Callback = "old"
	require.NoError(t, store.Create(ctx, old))
	<-waiting

	replacement := appTimer(t, "replace", "tick", "", now)
	replacement.Callback = "new"
	require.NoError(t, store.Create(ctx, replacement))
	waitReaped(t, store, replacement)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"new"}, fired)
}

func TestRescheduleKeepsTheTimerRemovalContext(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var seen []context.Context
	store, ok := New(Options{Router: routerfake.New().WithCallReminderFn(func(_ context.Context, r *api.Reminder) error {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, r.Removed)
		return nil
	})}).(*inmemory)
	require.True(t, ok)
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	ctx := context.Background()
	timer := newTimer(t, "x", "10ms", clock.RealClock{}.Now())
	require.NoError(t, store.Create(ctx, timer))
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seen) >= 3
	}, 5*time.Second, time.Millisecond)

	store.Delete(ctx, timer.Key())
	mu.Lock()
	defer mu.Unlock()
	require.NotNil(t, seen[0])
	for _, removed := range seen {
		assert.Same(t, seen[0], removed, "a rescheduled fire got a different removal context")
	}
	require.ErrorIs(t, seen[0].Err(), context.Canceled, "Delete did not cancel the removal context")
}

func TestCallbackDeletesOwnTimerCompletes(t *testing.T) {
	meter := newTimerMeter(t)
	var store *inmemory
	var timer *api.Reminder
	var calls atomic.Int64
	var alive atomic.Bool
	store, _ = newAppStore(t, "selfdelete", nil, func(ctx context.Context, req *invokev1.InvokeMethodRequest) {
		calls.Add(1)
		store.Delete(ctx, timer.Key())
		alive.Store(ctx.Err() == nil)
	})
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	timer = appTimer(t, "selfdelete", "tick", "1s", clock.RealClock{}.Now())
	require.NoError(t, store.Create(context.Background(), timer))
	waitReaped(t, store, timer)

	assert.Equal(t, int64(1), calls.Load())
	assert.True(t, alive.Load(), "deleting the timer cancelled its own running callback")
	assert.Equal(t, int64(1), timersSucceeded(t, meter))
	assert.Zero(t, timersDropped(t, meter))
	assert.Zero(t, timersFailed(t, meter))
}

func TestCallbackReregistersOwnTimerCompletes(t *testing.T) {
	t.Parallel()

	var store *inmemory
	var replacement *api.Reminder
	var calls atomic.Int64
	var alive atomic.Bool
	createErr := make(chan error, 1)
	store, _ = newAppStore(t, "selfreplace", nil, func(ctx context.Context, req *invokev1.InvokeMethodRequest) {
		calls.Add(1)
		createErr <- store.Create(ctx, replacement)
		alive.Store(ctx.Err() == nil)
	})
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	now := clock.RealClock{}.Now()
	timer := appTimer(t, "selfreplace", "tick", "1s", now)
	replacement = appTimer(t, "selfreplace", "tick", "1s", now.Add(time.Hour))
	require.NoError(t, store.Create(context.Background(), timer))
	require.Eventually(t, func() bool {
		_, pending, _ := stateSnapshot(store, timer.ActorKey())
		return calls.Load() == 1 && pending == 0
	}, 5*time.Second, time.Millisecond)

	require.NoError(t, <-createErr)
	assert.True(t, alive.Load(), "re-registering the timer cancelled its own running callback")
	cur, ok := store.activeTimers.Load(timer.Key())
	require.True(t, ok)
	assert.Same(t, replacement, cur)
}

func TestDeleteFuncDuringCallbackCompletes(t *testing.T) {
	t.Parallel()

	var store *inmemory
	var calls atomic.Int64
	var alive atomic.Bool
	store, _ = newAppStore(t, "sweepcallback", nil, func(ctx context.Context, req *invokev1.InvokeMethodRequest) {
		calls.Add(1)
		store.DeleteFunc(ctx, func(string, string) bool { return true })
		alive.Store(ctx.Err() == nil)
	})
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	timer := appTimer(t, "sweepcallback", "tick", "1s", clock.RealClock{}.Now())
	require.NoError(t, store.Create(context.Background(), timer))
	waitReaped(t, store, timer)

	assert.Equal(t, int64(1), calls.Load())
	assert.True(t, alive.Load(), "a sweep cancelled a running callback")
}
