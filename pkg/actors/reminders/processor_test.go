/*
Copyright 2021 The Dapr Authors
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
	"runtime"
	"strconv"
	"testing"
	"time"

	clocklib "github.com/benbjohnson/clock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessor(t *testing.T) {
	// Create the processor
	clock := clocklib.NewMock()
	executeCh := make(chan *Reminder)
	processor := NewProcessor(func(r *Reminder) {
		executeCh <- r
	}, clock)

	assertExecutedReminder := func(t *testing.T) *Reminder {
		t.Helper()

		// The signal is sent in a background goroutine, so we need to use a wall clock here
		runtime.Gosched()
		select {
		case r := <-executeCh:
			return r
		case <-time.After(700 * time.Millisecond):
			t.Fatal("did not receive signal in 700ms")
		}

		return nil
	}

	assertNoExecutedReminder := func(t *testing.T) {
		t.Helper()

		// The signal is sent in a background goroutine, so we need to use a wall clock here
		runtime.Gosched()
		select {
		case r := <-executeCh:
			t.Fatalf("received unexpected reminder: %s", r.Name)
		case <-time.After(500 * time.Millisecond):
			// all good
		}
	}

	// Makes tickers advance
	// Note that step must be > 500ms
	advanceTickers := func(step time.Duration, count int) {
		clock.Add(50 * time.Millisecond)
		// Sleep on the wall clock for a few ms to allow the background goroutine to get in sync (especially when testing with -race)
		runtime.Gosched()
		time.Sleep(50 * time.Millisecond)
		for i := 0; i < count; i++ {
			clock.Add(step)
			// Sleep on the wall clock for a few ms to allow the background goroutine to get in sync (especially when testing with -race)
			runtime.Gosched()
			time.Sleep(50 * time.Millisecond)
		}
	}

	t.Run("enqueue reminders", func(t *testing.T) {
		for i := 1; i <= 5; i++ {
			err := processor.Enqueue(
				newTestReminder(i, clock.Now().Add(time.Second*time.Duration(i))),
			)
			require.NoError(t, err)
		}

		// Advance tickers by 500ms to start
		advanceTickers(500*time.Millisecond, 1)

		// Advance tickers and assert messages are coming in order
		for i := 1; i <= 5; i++ {
			t.Logf("Waiting for signal %d", i)
			advanceTickers(time.Second, 1)
			received := assertExecutedReminder(t)
			assert.Equal(t, strconv.Itoa(i), received.Name)
		}
	})

	t.Run("enqueue reminder to be executed right away", func(t *testing.T) {
		r := newTestReminder(1, clock.Now())
		err := processor.Enqueue(r)
		require.NoError(t, err)

		advanceTickers(500*time.Millisecond, 1)

		received := assertExecutedReminder(t)
		assert.Equal(t, "1", received.Name)
	})

	t.Run("enqueue reminder at the front of the queue", func(t *testing.T) {
		// Enqueue 4 reminders
		for i := 1; i <= 4; i++ {
			err := processor.Enqueue(
				newTestReminder(i, clock.Now().Add(time.Second*time.Duration(i))),
			)
			require.NoError(t, err)
		}

		// Advance tickers by 1500ms to trigger the first reminder
		t.Log("Waiting for signal 1")
		advanceTickers(500*time.Millisecond, 3)

		received := assertExecutedReminder(t)
		assert.Equal(t, "1", received.Name)

		// Add a new reminder at the front of the queue
		err := processor.Enqueue(
			newTestReminder(99, clock.Now()),
		)
		require.NoError(t, err)

		// Advance tickers and assert messages are coming in order
		for i := 1; i <= 4; i++ {
			// First reminder should be 99
			expect := strconv.Itoa(i)
			if i == 1 {
				expect = "99"
			}
			t.Logf("Waiting for signal %s", expect)
			advanceTickers(time.Second, 1)
			received := assertExecutedReminder(t)
			assert.Equal(t, expect, received.Name)
		}
	})

	t.Run("dequeue reminder", func(t *testing.T) {
		// Enqueue 5 reminders
		for i := 1; i <= 5; i++ {
			err := processor.Enqueue(
				newTestReminder(i, clock.Now().Add(time.Second*time.Duration(i))),
			)
			require.NoError(t, err)
		}

		// Advance tickers by a few ms to start
		advanceTickers(0, 0)

		// Dequeue reminders 2 and 4
		err := processor.Dequeue(newTestReminder(2, 0)) // Time is irrelevant
		require.NoError(t, err)
		err = processor.Dequeue(newTestReminder(4, 0)) // Time is irrelevant
		require.NoError(t, err)

		// Advance tickers and assert messages are coming in order
		for i := 1; i <= 5; i++ {
			if i == 2 || i == 4 {
				// Skip reminders that have been removed
				t.Logf("Should not receive signal %d", i)
				advanceTickers(time.Second, 1)
				assertNoExecutedReminder(t)
				continue
			}
			t.Logf("Waiting for signal %d", i)
			advanceTickers(time.Second, 1)
			received := assertExecutedReminder(t)
			assert.Equal(t, strconv.Itoa(i), received.Name)
		}
	})

	t.Run("dequeue reminder from the front of the queue", func(t *testing.T) {
		// Enqueue 6 reminders
		for i := 1; i <= 6; i++ {
			err := processor.Enqueue(
				newTestReminder(i, clock.Now().Add(time.Second*time.Duration(i))),
			)
			require.NoError(t, err)
		}

		// Advance tickers by a few ms to start
		advanceTickers(0, 0)

		// Advance tickers and assert messages are coming in order
		for i := 1; i <= 6; i++ {
			// On messages 2 and 5, dequeue the reminder when it's at the front of the queue
			if i == 2 || i == 5 {
				// Dequeue the reminder at the front of the queue
				err := processor.Dequeue(newTestReminder(i, 0)) // Time is irrelevant
				require.NoError(t, err)

				// Skip reminders that have been removed
				t.Logf("Should not receive signal %d", i)
				advanceTickers(time.Second, 1)
				assertNoExecutedReminder(t)
				continue
			}
			t.Logf("Waiting for signal %d", i)
			advanceTickers(time.Second, 1)
			received := assertExecutedReminder(t)
			assert.Equal(t, strconv.Itoa(i), received.Name)
		}
	})

	t.Run("replace reminder", func(t *testing.T) {
		// Enqueue 5 reminders
		for i := 1; i <= 5; i++ {
			err := processor.Enqueue(
				newTestReminder(i, clock.Now().Add(time.Second*time.Duration(i))),
			)
			require.NoError(t, err)
		}

		// Replace reminder 4, bumping its priority down
		err := processor.Enqueue(newTestReminder(4, clock.Now().Add(6*time.Second)))
		require.NoError(t, err)

		// Advance tickers by a few ms to start
		advanceTickers(0, 0)

		// Advance tickers and assert messages are coming in order
		for i := 1; i <= 6; i++ {
			if i == 4 {
				// This reminder has been pushed down
				t.Logf("Should not receive signal %d now", i)
				advanceTickers(time.Second, 1)
				assertNoExecutedReminder(t)
				continue
			}

			expect := i
			if i == 6 {
				// Reminder 4 should come now
				expect = 4
			}
			t.Logf("Waiting for signal %d", expect)
			advanceTickers(time.Second, 1)
			received := assertExecutedReminder(t)
			assert.Equal(t, strconv.Itoa(expect), received.Name)
		}
	})

	t.Run("replace reminder at the front of the queue", func(t *testing.T) {
		// Enqueue 5 reminders
		for i := 1; i <= 5; i++ {
			err := processor.Enqueue(
				newTestReminder(i, clock.Now().Add(time.Second*time.Duration(i))),
			)
			require.NoError(t, err)
		}

		// Advance tickers by a few ms to start
		advanceTickers(0, 0)

		// Advance tickers and assert messages are coming in order
		for i := 1; i <= 6; i++ {
			if i == 2 {
				// Replace reminder 2, bumping its priority down, while it's at the front of the queue
				err := processor.Enqueue(newTestReminder(2, clock.Now().Add(5*time.Second)))
				require.NoError(t, err)

				// This reminder has been pushed down
				t.Logf("Should not receive signal %d now", i)
				advanceTickers(time.Second, 1)
				assertNoExecutedReminder(t)
				continue
			}

			expect := i
			if i == 6 {
				// Reminder 2 should come now
				expect = 2
			}
			t.Logf("Waiting for signal %d", expect)
			advanceTickers(time.Second, 1)
			received := assertExecutedReminder(t)
			assert.Equal(t, strconv.Itoa(expect), received.Name)
		}
	})

	t.Run("stop processor", func(t *testing.T) {
		// Enqueue 5 reminders
		for i := 1; i <= 5; i++ {
			err := processor.Enqueue(
				newTestReminder(i, clock.Now().Add(time.Second*time.Duration(i))),
			)
			require.NoError(t, err)
		}

		// Advance tickers by a few ms to start
		advanceTickers(0, 0)

		// Stop the processor
		processor.Stop()

		// Queue should not be processed
		advanceTickers(time.Second, 2)
		assertNoExecutedReminder(t)

		// Enqueuing and dequeueing should fail
		err := processor.Enqueue(newTestReminder(99, clock.Now()))
		require.ErrorIs(t, err, ErrProcessorStopped)
		err = processor.Dequeue(newTestReminder(99, clock.Now()))
		require.ErrorIs(t, err, ErrProcessorStopped)

		// Stopping again is a nop (should not crash)
		processor.Stop()
	})
}
