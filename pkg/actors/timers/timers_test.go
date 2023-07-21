/*
Copyright 2023 The Dapr Authors
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

package timers

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/dapr/dapr/pkg/actors/internal"
)

var startOfTime = time.Date(2022, 1, 1, 12, 0, 0, 0, time.UTC)

func TestCreateTimerDueTimes(t *testing.T) {
	clock := clocktesting.NewFakeClock(startOfTime)
	provider := NewTimersProvider(clock).(*timers)

	executed := make(chan string, 1)
	provider.SetExecuteTimerFn(func(reminder *internal.Reminder) bool {
		executed <- reminder.Key()
		return true
	})

	t.Run("create timer with positive DueTime", func(t *testing.T) {
		req := internal.CreateTimerRequest{
			ActorID:   "myactor",
			ActorType: "mytype",
			Name:      "mytimer",
			DueTime:   "1s",
			Callback:  "callback",
		}
		timer := createTimer(t, clock.Now(), req)

		err := provider.CreateTimer(context.Background(), timer)
		require.NoError(t, err)

		assert.Equal(t, int64(1), provider.GetActiveTimersCount("mytype"))
		_, ok := provider.activeTimers.Load(req.Key())
		assert.True(t, ok)

		advanceTickers(t, clock, 2*time.Second)
		select {
		case val := <-executed:
			assert.Equal(t, req.Key(), val)
		case <-time.After(10 * time.Second):
			t.Fatal("Did not receive a signal in time")
		}
	})

	t.Run("create timer with 0 DueTime", func(t *testing.T) {
		req := internal.CreateTimerRequest{
			ActorID:   "myactor",
			ActorType: "mytype",
			Name:      "mytimer",
			DueTime:   "0",
			Callback:  "callback",
		}
		timer := createTimer(t, clock.Now(), req)

		err := provider.CreateTimer(context.Background(), timer)
		require.NoError(t, err)

		assert.Equal(t, int64(1), provider.GetActiveTimersCount("mytype"))
		_, ok := provider.activeTimers.Load(req.Key())
		assert.True(t, ok)

		advanceTickers(t, clock, 500*time.Millisecond)
		select {
		case val := <-executed:
			assert.Equal(t, req.Key(), val)
		case <-time.After(10 * time.Second):
			t.Fatal("Did not receive a signal in time")
		}
	})

	t.Run("create timer with no DueTime", func(t *testing.T) {
		req := internal.CreateTimerRequest{
			ActorID:   "myactor",
			ActorType: "mytype",
			Name:      "mytimer",
			DueTime:   "",
			Callback:  "callback",
		}
		timer := createTimer(t, clock.Now(), req)

		err := provider.CreateTimer(context.Background(), timer)
		require.NoError(t, err)

		assert.Equal(t, int64(1), provider.GetActiveTimersCount("mytype"))
		_, ok := provider.activeTimers.Load(req.Key())
		assert.True(t, ok)

		advanceTickers(t, clock, 500*time.Millisecond)
		select {
		case val := <-executed:
			assert.Equal(t, req.Key(), val)
		case <-time.After(10 * time.Second):
			t.Fatal("Did not receive a signal in time")
		}
	})
}

func TestDeleteTimer(t *testing.T) {
	clock := clocktesting.NewFakeClock(startOfTime)
	provider := NewTimersProvider(clock).(*timers)

	req := internal.CreateTimerRequest{
		ActorID:   "myactor",
		ActorType: "mytype",
		Name:      "mytimer",
		DueTime:   "100ms",
		Callback:  "callback",
	}
	timer := createTimer(t, clock.Now(), req)

	err := provider.CreateTimer(context.Background(), timer)
	require.NoError(t, err)
	assert.Equal(t, int64(1), provider.GetActiveTimersCount(req.ActorType))

	err = provider.DeleteTimer(context.Background(), req.Key())
	require.NoError(t, err)

	assert.Eventuallyf(t,
		func() bool {
			return provider.GetActiveTimersCount(req.ActorType) == 0
		},
		10*time.Second, 50*time.Millisecond,
		"Expected active timers count to be %d, but got %d (note: this value may be outdated)", 0, provider.GetActiveTimersCount(req.ActorType),
	)
}

func TestOverrideTimer(t *testing.T) {
	clock := clocktesting.NewFakeClock(startOfTime)
	provider := NewTimersProvider(clock).(*timers)

	executed := make(chan string, 1)
	provider.SetExecuteTimerFn(func(reminder *internal.Reminder) bool {
		executed <- string(reminder.Data)
		return true
	})

	t.Run("override data", func(t *testing.T) {
		timer1 := createTimer(t, clock.Now(), internal.CreateTimerRequest{
			ActorID:   "myactor",
			ActorType: "mytype",
			Name:      "mytimer",
			DueTime:   "10s",
			Callback:  "callback1",
			Data:      json.RawMessage("1"),
		})
		err := provider.CreateTimer(context.Background(), timer1)
		require.NoError(t, err)

		timer2 := createTimer(t, clock.Now(), internal.CreateTimerRequest{
			ActorID:   "myactor",
			ActorType: "mytype",
			Name:      "mytimer",
			DueTime:   "PT9S",
			Callback:  "callback2",
			Data:      json.RawMessage("2"),
		})
		err = provider.CreateTimer(context.Background(), timer2)
		require.NoError(t, err)

		timer3 := createTimer(t, clock.Now(), internal.CreateTimerRequest{
			ActorID:   "myactor",
			ActorType: "mytype",
			Name:      "mytimer",
			DueTime:   "2s",
			Callback:  "callback3",
			Data:      json.RawMessage("3"),
		})
		err = provider.CreateTimer(context.Background(), timer3)
		require.NoError(t, err)

		// due time for timer3 is 2s
		advanceTickers(t, clock, time.Second)
		advanceTickers(t, clock, time.Second)

		// The timer update fires in a goroutine so we need to use the wall clock here
		select {
		case val := <-executed:
			assert.Equal(t, "3", val)
		case <-time.After(10 * time.Second):
			t.Fatal("Did not receive a signal in time")
		}
	})
}

func TestTimerCounter(t *testing.T) {
	const actorType = "mytype"
	const actorID = "myactor"

	clock := clocktesting.NewFakeClock(startOfTime)
	provider := NewTimersProvider(clock).(*timers)

	// Init a mock metrics collector
	activeCount := atomic.Int64{}
	invalidInvocations := atomic.Int64{}
	provider.SetMetricsCollector(func(at string, r int64) {
		if at == actorType {
			activeCount.Store(r)
		} else {
			invalidInvocations.Add(1)
		}
	})

	// Count executions
	executeCount := atomic.Int64{}
	provider.SetExecuteTimerFn(func(_ *internal.Reminder) bool {
		executeCount.Add(1)
		return true
	})

	const numberOfLongTimersToCreate = 755
	const numberOfOneTimeTimersToCreate = 220
	const numberOfTimersToDelete = 255

	var wg sync.WaitGroup

	for i := 0; i < numberOfLongTimersToCreate; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			timer := createTimer(t, clock.Now(), internal.CreateTimerRequest{
				ActorID:   actorID,
				ActorType: actorType,
				Name:      fmt.Sprintf("positiveTimer%d", idx),
				Period:    "R10/PT1S",
				DueTime:   "500ms",
				Callback:  "callback",
				Data:      json.RawMessage(`"testTimer"`),
			})
			err := provider.CreateTimer(context.Background(), timer)
			assert.NoError(t, err)
		}(i)
	}
	for i := 0; i < numberOfOneTimeTimersToCreate; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			timer := createTimer(t, clock.Now(), internal.CreateTimerRequest{
				ActorID:   actorID,
				ActorType: actorType,
				Name:      fmt.Sprintf("positiveTimerOneTime%d", idx),
				DueTime:   "500ms",
				Callback:  "callback",
				Data:      json.RawMessage(`"testTimer"`),
			})
			err := provider.CreateTimer(context.Background(), timer)
			assert.NoError(t, err)
		}(i)
	}
	wg.Wait()

	time.Sleep(1 * time.Second)
	clock.Sleep(1000 * time.Millisecond)

	for i := 0; i < numberOfTimersToDelete; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			timer := internal.Reminder{
				ActorID:   actorID,
				ActorType: actorType,
				Name:      fmt.Sprintf("positiveTimer%d", idx),
			}
			err := provider.DeleteTimer(context.Background(), timer.Key())
			assert.NoError(t, err)
		}(i)
	}
	wg.Wait()

	expectCount := int64(numberOfLongTimersToCreate - numberOfTimersToDelete)
	assert.Eventuallyf(t,
		func() bool {
			return provider.GetActiveTimersCount(actorType) == expectCount
		},
		10*time.Second, 50*time.Millisecond,
		"Expected active timers count to be %d, but got %d (note: this value may be outdated)", expectCount, provider.GetActiveTimersCount(actorType),
	)

	assert.Equal(t, int64(0), invalidInvocations.Load())
	assert.Equal(t, expectCount, activeCount.Load())

	assert.Equal(t, int64(numberOfLongTimersToCreate+numberOfOneTimeTimersToCreate), executeCount.Load())
}

func TestCreateTimerGoroutineLeak(t *testing.T) {
	clock := clocktesting.NewFakeClock(startOfTime)
	provider := NewTimersProvider(clock).(*timers)

	createFn := func(i int, ttl bool) error {
		req := &internal.CreateTimerRequest{
			ActorID:   "myactor",
			ActorType: "mytype",
			Name:      fmt.Sprintf("timer%d", i),
			Data:      json.RawMessage(`"data"`),
			DueTime:   "2s",
		}
		if ttl {
			req.DueTime = "1s"
			req.Period = "1s"
			req.TTL = "2s"
		}
		r, err := req.NewReminder(clock.Now())
		if err != nil {
			return err
		}
		return provider.CreateTimer(context.Background(), r)
	}

	// Get the baseline goroutines
	initialCount := runtime.NumGoroutine()

	// Create 10 timers with unique names
	for i := 0; i < 10; i++ {
		require.NoError(t, createFn(i, false))
	}

	// Create 5 timers that override the first ones
	for i := 0; i < 5; i++ {
		require.NoError(t, createFn(i, false))
	}

	// Create 5 timers that have TTLs
	for i := 10; i < 15; i++ {
		require.NoError(t, createFn(i, true))
	}

	// Advance the clock to make the timers fire
	time.Sleep(150 * time.Millisecond)
	clock.Sleep(5 * time.Second)
	time.Sleep(150 * time.Millisecond)
	clock.Sleep(5 * time.Second)

	// Get the number of goroutines again, which should be +/- 2 the initial one (we give it some buffer)
	require.Eventuallyf(t, func() bool {
		currentCount := runtime.NumGoroutine()
		return currentCount < (initialCount+2) && currentCount > (initialCount-2)
	}, time.Second, 50*time.Millisecond, "Current number of goroutine %[1]d is outside of range [%[2]d-2, %[2]d+2] (current count may be stale)", time.Duration(runtime.NumGoroutine()), initialCount)
}

func createTimer(t *testing.T, now time.Time, req internal.CreateTimerRequest) *internal.Reminder {
	t.Helper()

	reminder, err := req.NewReminder(now)
	require.NoError(t, err)

	return reminder
}

// Makes tickers advance
func advanceTickers(t *testing.T, clock *clocktesting.FakeClock, step time.Duration) {
	t.Helper()

	// Wait for the clock to have tickers before stepping, since they are likely
	// being created in another go routine to this test.
	require.Eventually(t, func() bool {
		return clock.HasWaiters()
	}, 2*time.Second, 5*time.Millisecond, "ticker in program not created in time")
	clock.Step(step)
}
