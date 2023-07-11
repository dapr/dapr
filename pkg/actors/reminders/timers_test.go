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

package reminders

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clocktesting "k8s.io/utils/clock/testing"
)

var startOfTime = time.Date(2022, 1, 1, 12, 0, 0, 0, time.UTC)

func TestCreateTimerDueTimes(t *testing.T) {
	clock := clocktesting.NewFakeClock(startOfTime)
	provider := NewTimersProvider(clock).(*timers)

	executed := make(chan string, 1)
	provider.SetExecuteTimerFn(func(reminder *Reminder) bool {
		executed <- reminder.Key()
		return true
	})

	t.Run("create timer with positive DueTime", func(t *testing.T) {
		req := CreateTimerRequest{
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

	t.Run("test create timer with 0 DueTime", func(t *testing.T) {
		req := CreateTimerRequest{
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

		/*// Should not have waiters even after 200ms (on a wall clock to ensure background goroutines sync)
		runtime.Gosched()
		time.Sleep(200 * time.Millisecond)
		assert.False(t, clock.HasWaiters())*/
	})

	t.Run("test create timer with no DueTime", func(t *testing.T) {
		req := CreateTimerRequest{
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

	req := CreateTimerRequest{
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
	provider.SetExecuteTimerFn(func(reminder *Reminder) bool {
		executed <- string(reminder.Data)
		return true
	})

	t.Run("override data", func(t *testing.T) {
		timer1 := createTimer(t, clock.Now(), CreateTimerRequest{
			ActorID:   "myactor",
			ActorType: "mytype",
			Name:      "mytimer",
			DueTime:   "10s",
			Callback:  "callback1",
			Data:      json.RawMessage("1"),
		})
		err := provider.CreateTimer(context.Background(), timer1)
		require.NoError(t, err)

		timer2 := createTimer(t, clock.Now(), CreateTimerRequest{
			ActorID:   "myactor",
			ActorType: "mytype",
			Name:      "mytimer",
			DueTime:   "PT9S",
			Callback:  "callback2",
			Data:      json.RawMessage("2"),
		})
		err = provider.CreateTimer(context.Background(), timer2)
		require.NoError(t, err)

		timer3 := createTimer(t, clock.Now(), CreateTimerRequest{
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

func createTimer(t *testing.T, now time.Time, req CreateTimerRequest) *Reminder {
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
	}, time.Second, time.Millisecond, "ticker in program not created in time")
	clock.Step(step)
}
