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

const TestAppID = "fakeAppID"

func newTestTimers() *timers {
	clock := clocktesting.NewFakeClock(startOfTime)
	r := NewTimersProvider(clock).(*timers)
	r.processor.WithClock(clock)
	return r
}

// testRequest is the request object that encapsulates the `data` field of a request.
type testRequest struct {
	Data any `json:"data"`
}

func TestCreateTimerDueTimes(t *testing.T) {
	t.Run("create timer with positive DueTime", func(t *testing.T) {
		clock := clocktesting.NewFakeClock(startOfTime)
		provider := NewTimersProvider(clock).(*timers)
		provider.processor.WithClock(clock)
		defer provider.Close()

		executed := make(chan string, 1)
		provider.SetExecuteTimerFn(func(reminder *internal.Reminder) bool {
			executed <- reminder.Key()
			return true
		})
		require.NoError(t, provider.Init(context.Background()))

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
		clock := clocktesting.NewFakeClock(startOfTime)
		provider := NewTimersProvider(clock).(*timers)
		provider.processor.WithClock(clock)
		defer provider.Close()

		executed := make(chan string, 1)
		provider.SetExecuteTimerFn(func(reminder *internal.Reminder) bool {
			executed <- reminder.Key()
			return true
		})
		require.NoError(t, provider.Init(context.Background()))

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
		clock := clocktesting.NewFakeClock(startOfTime)
		provider := NewTimersProvider(clock).(*timers)
		provider.processor.WithClock(clock)
		defer provider.Close()

		executed := make(chan string, 1)
		provider.SetExecuteTimerFn(func(reminder *internal.Reminder) bool {
			executed <- reminder.Key()
			return true
		})
		require.NoError(t, provider.Init(context.Background()))

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
	testTimers := newTestTimers()
	defer testTimers.Close()
	require.NoError(t, testTimers.Init(context.Background()))

	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()

	req := createTimerData(actorID, actorType, "timer1", "100ms", "100ms", "", "callback", "")
	reminder, err := req.NewReminder(testTimers.clock.Now())
	require.NoError(t, err)
	err = testTimers.CreateTimer(ctx, reminder)
	require.NoError(t, err)

	assert.Equal(t, int64(1), testTimers.GetActiveTimersCount(actorType))

	err = testTimers.DeleteTimer(ctx, req.Key())
	require.NoError(t, err)

	assert.Eventuallyf(t,
		func() bool {
			return testTimers.GetActiveTimersCount(actorType) == 0
		},
		10*time.Second, 50*time.Millisecond,
		"Expected active timers count to be %d, but got %d (note: this value may be outdated)", 0, testTimers.GetActiveTimersCount(actorType),
	)
}

func TestOverrideTimerCancelsActiveTimers(t *testing.T) {
	ctx := context.Background()
	t.Run("override data", func(t *testing.T) {
		requestC := make(chan testRequest, 10)
		testTimers := newTestTimers()
		defer testTimers.Close()
		testTimers.SetExecuteTimerFn(func(reminder *internal.Reminder) bool {
			requestC <- testRequest{Data: "c"}
			return true
		})
		clock := testTimers.clock.(*clocktesting.FakeClock)
		require.NoError(t, testTimers.Init(context.Background()))

		actorType, actorID := getTestActorTypeAndID()
		timerName := "timer1"

		req := createTimerData(actorID, actorType, timerName, "10s", "1s", "0s", "callback1", "a")
		reminder, err := req.NewReminder(testTimers.clock.Now())
		require.NoError(t, err)
		err = testTimers.CreateTimer(ctx, reminder)
		require.NoError(t, err)

		req2 := createTimerData(actorID, actorType, timerName, "PT9S", "PT1S", "PT0S", "callback2", "b")
		reminder2, err := req2.NewReminder(testTimers.clock.Now())
		require.NoError(t, err)
		testTimers.CreateTimer(ctx, reminder2)
		require.NoError(t, err)

		req3 := createTimerData(actorID, actorType, timerName, "8s", "2s", "", "callback3", "c")
		reminder3, err := req3.NewReminder(testTimers.clock.Now())
		require.NoError(t, err)
		testTimers.CreateTimer(ctx, reminder3)
		require.NoError(t, err)

		// due time for timer3 is 2s
		advanceTickers(t, clock, 2*time.Second)

		// The timer update fires in a goroutine so we need to use the wall clock here
		select {
		case request := <-requestC:
			// Test that the last reminder update fired
			assert.Equal(t, string(req3.Data), "\""+request.Data.(string)+"\"")
		case <-time.After(1500 * time.Millisecond):
			assert.Fail(t, "request channel timed out")
		}
	})
}

func TestOverrideTimerCancelsMultipleActiveTimers(t *testing.T) {
	ctx := context.Background()
	t.Run("override data", func(t *testing.T) {
		requestC := make(chan testRequest, 10)
		testTimers := newTestTimers()
		defer testTimers.Close()
		testTimers.SetExecuteTimerFn(func(reminder *internal.Reminder) bool {
			requestC <- testRequest{Data: "d"}
			return true
		})
		clock := testTimers.clock.(*clocktesting.FakeClock)
		require.NoError(t, testTimers.Init(context.Background()))

		actorType, actorID := getTestActorTypeAndID()
		timerName := "timer1"

		req := createTimerData(actorID, actorType, timerName, "10s", "3s", "", "callback1", "a")
		reminder, err := req.NewReminder(testTimers.clock.Now())
		require.NoError(t, err)
		err = testTimers.CreateTimer(ctx, reminder)
		require.NoError(t, err)

		req2 := createTimerData(actorID, actorType, timerName, "8s", "4s", "", "callback2", "b")
		reminder2, err := req2.NewReminder(testTimers.clock.Now())
		require.NoError(t, err)
		testTimers.CreateTimer(ctx, reminder2)
		require.NoError(t, err)

		req3 := createTimerData(actorID, actorType, timerName, "8s", "4s", "", "callback3", "c")
		reminder3, err := req3.NewReminder(testTimers.clock.Now())
		require.NoError(t, err)
		testTimers.CreateTimer(ctx, reminder3)
		require.NoError(t, err)

		// due time for timer2/timer3 is 4s, advance less
		advanceTickers(t, clock, 2*time.Second)

		req4 := createTimerData(actorID, actorType, timerName, "7s", "2s", "", "callback4", "d")
		reminder4, err := req4.NewReminder(testTimers.clock.Now())
		require.NoError(t, err)
		testTimers.CreateTimer(ctx, reminder4)
		require.NoError(t, err)

		// due time for timer4 is 2s
		advanceTickers(t, clock, 2*time.Second)

		// The timer update fires in a goroutine so we need to use the wall clock here
		select {
		case request := <-requestC:
			// Test that the last reminder update fired
			assert.Equal(t, string(req4.Data), "\""+request.Data.(string)+"\"")
		case <-time.After(1500 * time.Millisecond):
			assert.Fail(t, "request channel timed out")
		}
	})
}

func TestTimerRepeats(t *testing.T) {
	tests := map[string]struct {
		dueTime         string
		period          string
		ttl             string
		expRepeats      int
		delAfterSeconds float64
	}{
		"timer with dueTime is ignored": {
			dueTime:         "2s",
			period:          "R0/PT2S",
			ttl:             "",
			expRepeats:      0,
			delAfterSeconds: 0,
		},
		"timer without dueTime is ignored": {
			dueTime:         "",
			period:          "R0/PT2S",
			ttl:             "",
			expRepeats:      0,
			delAfterSeconds: 0,
		},
		"timer with dueTime repeats once": {
			dueTime:         "2s",
			period:          "R1/PT2S",
			ttl:             "",
			expRepeats:      1,
			delAfterSeconds: 0,
		},
		"timer without dueTime repeats once": {
			dueTime:         "",
			period:          "R1/PT2S",
			ttl:             "",
			expRepeats:      1,
			delAfterSeconds: 0,
		},
		"timer with dueTime period not set": {
			dueTime:         "2s",
			period:          "",
			ttl:             "",
			expRepeats:      1,
			delAfterSeconds: 0,
		},
		"timer without dueTime period not set": {
			dueTime:         "",
			period:          "",
			ttl:             "",
			expRepeats:      1,
			delAfterSeconds: 0,
		},
		"timer with dueTime repeats 3 times": {
			dueTime:         "2s",
			period:          "R3/PT2S",
			ttl:             "",
			expRepeats:      3,
			delAfterSeconds: 0,
		},
		"timer without dueTime repeats 3 times": {
			dueTime:         "",
			period:          "R3/PT2S",
			ttl:             "",
			expRepeats:      3,
			delAfterSeconds: 0,
		},
		"timer with dueTime deleted after 1 sec": {
			dueTime:         startOfTime.Add(2 * time.Second).Format(time.RFC3339),
			period:          "PT4S",
			ttl:             "",
			expRepeats:      1,
			delAfterSeconds: 3,
		},
		"timer without dueTime deleted after 1 sec": {
			dueTime:         "",
			period:          "PT2S",
			ttl:             "",
			expRepeats:      1,
			delAfterSeconds: 1,
		},
		"timer with dueTime ttl": {
			dueTime:         startOfTime.Add(2 * time.Second).Format(time.RFC3339),
			period:          "PT2S",
			ttl:             "3s",
			expRepeats:      2,
			delAfterSeconds: 0,
		},
		"timer without dueTime ttl": {
			dueTime:         "",
			period:          "4s",
			ttl:             startOfTime.Add(6 * time.Second).Format(time.RFC3339),
			expRepeats:      2,
			delAfterSeconds: 0,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			requestC := make(chan testRequest, 10)
			testTimers := newTestTimers()
			defer testTimers.Close()
			testTimers.SetExecuteTimerFn(func(reminder *internal.Reminder) bool {
				requestC <- testRequest{Data: "data"}
				return true
			})
			require.NoError(t, testTimers.Init(context.Background()))

			clock := testTimers.clock.(*clocktesting.FakeClock)
			actorType, actorID := getTestActorTypeAndID()

			req := internal.CreateTimerRequest{
				ActorID:   actorID,
				ActorType: actorType,
				Name:      "timer",
				Period:    test.period,
				DueTime:   test.dueTime,
				TTL:       test.ttl,
				Data:      json.RawMessage(`"data"`),
				Callback:  "callback",
			}
			reminder, err := req.NewReminder(testTimers.clock.Now())
			if test.expRepeats == 0 {
				require.ErrorContains(t, err, "has zero repetitions")
				return
			}
			require.NoError(t, err)

			ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
			t.Cleanup(cancel)

			err = testTimers.CreateTimer(ctx, reminder)
			require.NoError(t, err)

			count := 0

			var wg sync.WaitGroup
			t.Cleanup(wg.Wait)
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer cancel()

				start := clock.Now()
				ticker := clock.NewTicker(time.Second)
				defer ticker.Stop()

				for i := 0; i < 10; i++ {
					if test.delAfterSeconds > 0 && clock.Now().Sub(start).Seconds() >= test.delAfterSeconds {
						require.NoError(t, testTimers.DeleteTimer(ctx, req.Key()))
					}

					select {
					case request := <-requestC:
						// Decrease i since time hasn't increased.
						i--
						assert.Equal(t, string(req.Data), "\""+request.Data.(string)+"\"")
						count++
					case <-ctx.Done():
					case <-ticker.C():
					}
				}
			}()

			for {
				select {
				case <-ctx.Done():
					require.Equal(t, test.expRepeats, count)
					return
				case <-time.After(time.Millisecond):
					advanceTickers(t, clock, time.Millisecond*500)
				}
			}
		})
	}
}

func TestTimerTTL(t *testing.T) {
	tests := map[string]struct {
		iso bool
	}{
		"timer ttl": {
			iso: false,
		},
		"timer ttl with ISO 8601": {
			iso: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			requestC := make(chan testRequest, 10)
			testTimers := newTestTimers()
			defer testTimers.Close()
			testTimers.SetExecuteTimerFn(func(reminder *internal.Reminder) bool {
				requestC <- testRequest{Data: "data"}
				return true
			})
			clock := testTimers.clock.(*clocktesting.FakeClock)
			require.NoError(t, testTimers.Init(context.Background()))

			actorType, actorID := getTestActorTypeAndID()

			ttl := "7s"
			if test.iso {
				ttl = "PT7S"
			}
			req := createTimerData(actorID, actorType, "timer", "R5/PT2S", "2s", ttl, "callback", "data")
			reminder, err := req.NewReminder(testTimers.clock.Now())
			require.NoError(t, err)

			ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
			t.Cleanup(cancel)
			require.NoError(t, testTimers.CreateTimer(ctx, reminder))

			count := 0

			ticker := clock.NewTicker(time.Second)
			defer ticker.Stop()

			advanceTickers(t, clock, 0)

			var wg sync.WaitGroup
			t.Cleanup(wg.Wait)
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer cancel()

				for i := 0; i < 10; i++ {
					select {
					case request := <-requestC:
						// Decrease i since time hasn't increased.
						i--
						assert.Equal(t, string(req.Data), "\""+request.Data.(string)+"\"")
						count++
					case <-ticker.C():
						// nop
					}
				}
			}()

			for {
				select {
				case <-ctx.Done():
					assert.Equal(t, 4, count)
					return
				case <-time.After(time.Millisecond):
					advanceTickers(t, clock, time.Millisecond*500)
				}
			}
		})
	}
}

func timerValidation(dueTime, period, ttl, msg string) func(t *testing.T) {
	return func(t *testing.T) {
		testTimers := newTestTimers()
		defer testTimers.Close()
		require.NoError(t, testTimers.Init(context.Background()))

		actorType, actorID := getTestActorTypeAndID()

		req := createTimerData(actorID, actorType, "timer", period, dueTime, ttl, "callback", "data")
		reminder, err := req.NewReminder(testTimers.clock.Now())
		if err == nil {
			err = testTimers.CreateTimer(context.Background(), reminder)
		}
		require.ErrorContains(t, err, msg)
	}
}

func TestTimerValidation(t *testing.T) {
	t.Run("timer dueTime invalid (1)", timerValidation("invalid", "R5/PT2S", "1h", "unsupported time/duration format: invalid"))
	t.Run("timer dueTime invalid (2)", timerValidation("R5/PT2S", "R5/PT2S", "1h", "repetitions are not allowed"))
	t.Run("timer period invalid", timerValidation(startOfTime.Add(time.Minute).Format(time.RFC3339), "invalid", "1h", "unsupported duration format: invalid"))
	t.Run("timer ttl invalid (1)", timerValidation("", "", "invalid", "unsupported time/duration format: invalid"))
	t.Run("timer ttl invalid (2)", timerValidation("", "", "R5/PT2S", "repetitions are not allowed"))
	t.Run("timer ttl expired (1)", timerValidation("2s", "", "-2s", "has already expired"))
	t.Run("timer ttl expired (2)", timerValidation("", "", "-2s", "has already expired"))
	t.Run("timer ttl expired (3)", timerValidation(startOfTime.Add(2*time.Second).Format(time.RFC3339), "", startOfTime.Add(time.Second).Format(time.RFC3339), "has already expired"))
	t.Run("timer ttl expired (4)", timerValidation("", "", startOfTime.Add(-1*time.Second).Format(time.RFC3339), "has already expired"))
}

func TestOverrideTimer(t *testing.T) {
	clock := clocktesting.NewFakeClock(startOfTime)
	provider := NewTimersProvider(clock).(*timers)
	provider.processor.WithClock(clock)
	defer provider.Close()

	executed := make(chan string, 1)
	provider.SetExecuteTimerFn(func(reminder *internal.Reminder) bool {
		executed <- string(reminder.Data)
		return true
	})
	require.NoError(t, provider.Init(context.Background()))

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

		expectCount := int64(1)
		assert.Eventuallyf(t,
			func() bool {
				return provider.GetActiveTimersCount("mytype") == expectCount
			},
			5*time.Second, 50*time.Millisecond,
			"Expected active timers count to be %d, but got %d (note: this value may be outdated)", expectCount, provider.GetActiveTimersCount("mytype"),
		)

		// Due time for timer3 is 2s
		advanceTickers(t, clock, 2*time.Second)

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
	const (
		actorType = "mytype"
		actorID   = "myactor"
	)

	clock := clocktesting.NewFakeClock(startOfTime)
	provider := NewTimersProvider(clock).(*timers)
	provider.processor.WithClock(clock)
	defer provider.Close()

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

	require.NoError(t, provider.Init(context.Background()))

	const (
		numberOfLongTimersToCreate    = 755
		numberOfOneTimeTimersToCreate = 220
		numberOfTimersToDelete        = 255
	)

	var wg sync.WaitGroup

	now := clock.Now()
	for i := 0; i < numberOfLongTimersToCreate; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			timer := createTimer(t, now, internal.CreateTimerRequest{
				ActorID:   actorID,
				ActorType: actorType,
				Name:      fmt.Sprintf("positiveTimer%d", idx),
				Period:    "R10/PT1S",
				DueTime:   "1s",
				Callback:  "callback",
				Data:      json.RawMessage(`"testTimer"`),
			})
			err := provider.CreateTimer(context.Background(), timer)
			require.NoError(t, err)
		}(i)
	}

	for i := 0; i < numberOfOneTimeTimersToCreate; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			timer := createTimer(t, now, internal.CreateTimerRequest{
				ActorID:   actorID,
				ActorType: actorType,
				Name:      fmt.Sprintf("positiveTimerOneTime%d", idx),
				DueTime:   "1s",
				Callback:  "callback",
				Data:      json.RawMessage(`"testTimer"`),
			})
			err := provider.CreateTimer(context.Background(), timer)
			require.NoError(t, err)
		}(i)
	}

	wg.Wait()

	// Allow background goroutines to catch up
	advanceTickers(t, clock, 100*time.Millisecond)

	expectCount := int64(numberOfLongTimersToCreate + numberOfOneTimeTimersToCreate)
	assert.Eventuallyf(t,
		func() bool {
			return provider.GetActiveTimersCount(actorType) == expectCount
		},
		5*time.Second, 50*time.Millisecond,
		"Expected active timers count to be %d, but got %d (note: this value may be outdated)", expectCount, provider.GetActiveTimersCount(actorType),
	)

	// Advance the timers by 1s to make all short timers execute
	clock.Step(time.Second)

	expectExecute := int64(numberOfLongTimersToCreate + numberOfOneTimeTimersToCreate)
	assert.Eventuallyf(t,
		func() bool {
			return executeCount.Load() == expectExecute
		},
		5*time.Second, 50*time.Millisecond,
		"Expected to have %d reminders executed, but got %d (note: this value may be outdated)", expectExecute, executeCount.Load(),
	)

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
			require.NoError(t, err)
		}(i)
	}

	wg.Wait()

	expectCount = int64(numberOfLongTimersToCreate - numberOfTimersToDelete)
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
	provider.processor.WithClock(clock)
	defer provider.Close()
	require.NoError(t, provider.Init(context.Background()))

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

	// Sleep a bit on the wall clock too to help advance other goroutines
	runtime.Gosched()
	time.Sleep(100 * time.Millisecond)
	clock.Step(step)
}

func getTestActorTypeAndID() (string, string) {
	return "cat", "e485d5de-de48-45ab-816e-6cc700d18ace"
}

func createTimerData(actorID, actorType, name, period, dueTime, ttl, callback, data string) internal.CreateTimerRequest {
	r := internal.CreateTimerRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Name:      name,
		Period:    period,
		DueTime:   dueTime,
		TTL:       ttl,
		Callback:  callback,
	}
	if data != "" {
		r.Data = json.RawMessage(`"` + data + `"`)
	}
	return r
}
