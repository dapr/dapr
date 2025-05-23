/*
Copyright 2022 The Dapr Authors
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

package apphealth

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/dapr/dapr/pkg/config"
)

func TestAppHealth_setResult(t *testing.T) {
	var threshold int32 = 3
	h := New(config.AppHealthConfig{
		Threshold: threshold,
	}, nil)

	// Set the initial state to healthy
	h.setResult(t.Context(), NewStatus(true, nil))

	statusChange := make(chan *Status, 1)
	unexpectedStatusChanges := atomic.Int32{}
	h.OnHealthChange(func(ctx context.Context, status *Status) {
		select {
		case statusChange <- status:
			// Do nothing
		default:
			// If the channel is full, it means we were not expecting a status change
			unexpectedStatusChanges.Add(1)
		}
	})

	simulateFailures := func(n int32) {
		statusChange <- NewStatus(false, nil) // Fill the channel
		for i := range n {
			if i == threshold-1 {
				<-statusChange // Allow the channel to be written into
			}
			h.setResult(t.Context(), NewStatus(false, nil))

			if i == threshold-1 {
				select {
				case v := <-statusChange:
					assert.False(t, v.IsHealthy)
				case <-time.After(100 * time.Millisecond):
					t.Error("did not get a status change before deadline")
				}
				statusChange <- NewStatus(false, nil) // Fill the channel again
			} else {
				time.Sleep(time.Millisecond)
			}
		}
	}

	// Trigger threshold+10 failures and detect the invocation after the threshold
	simulateFailures(threshold + 10)
	assert.Empty(t, unexpectedStatusChanges.Load())
	assert.Equal(t, threshold+10, h.failureCount.Load())

	// First success should bring the app back to healthy
	<-statusChange // Allow the channel to be written into
	h.setResult(t.Context(), NewStatus(true, nil))
	select {
	case v := <-statusChange:
		assert.True(t, v.IsHealthy)
	case <-time.After(100 * time.Millisecond):
		t.Error("did not get a status change before deadline")
	}
	assert.Equal(t, int32(0), h.failureCount.Load())

	// Multiple invocations in parallel
	// Only one failure should be sent
	wg := sync.WaitGroup{}
	for range 5 {
		wg.Add(1)
		go func() {
			for range threshold + 5 {
				h.setResult(t.Context(), NewStatus(false, nil))
			}
			wg.Done()
		}()
	}
	wg.Wait()

	select {
	case v := <-statusChange:
		assert.False(t, v.IsHealthy)
	case <-time.After(50 * time.Millisecond):
		t.Error("did not get a status change before deadline")
	}

	assert.Empty(t, unexpectedStatusChanges.Load())
	assert.Equal(t, (threshold+5)*5, h.failureCount.Load())

	// Test overflows
	h.failureCount.Store(int32(math.MaxInt32 - 2))
	statusChange <- NewStatus(false, nil) // Fill the channel again
	for range 5 {
		h.setResult(t.Context(), NewStatus(false, nil))
	}
	assert.Empty(t, unexpectedStatusChanges.Load())
	assert.Equal(t, threshold+3, h.failureCount.Load())
}

func Test_StartProbes(t *testing.T) {
	t.Run("closing context should return", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		done := make(chan struct{})

		h := New(config.AppHealthConfig{
			ProbeInterval: time.Second,
		}, func(context.Context) (*Status, error) {
			assert.Fail(t, "unexpected probe call")
			return NewStatus(false, nil), nil
		})
		clock := clocktesting.NewFakeClock(time.Now())
		h.clock = clock

		go func() {
			defer close(done)
			assert.NoError(t, h.StartProbes(ctx))
		}()

		// Wait for ticker to start,
		assert.Eventually(t, clock.HasWaiters, time.Second, time.Microsecond)
		cancel()

		select {
		case <-done:
		case <-time.After(time.Millisecond * 100):
			assert.Fail(t, "StartProbes didn't return in time")
		}
	})

	t.Run("calling StartProbes after it has already closed should error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		h := New(config.AppHealthConfig{
			ProbeInterval: time.Second,
		}, func(context.Context) (*Status, error) {
			assert.Fail(t, "unexpected probe call")
			return NewStatus(false, nil), nil
		})

		h.Close()

		done := make(chan struct{})
		go func() {
			defer close(done)
			assert.Error(t, h.StartProbes(ctx))
		}()

		select {
		case <-done:
		case <-time.After(time.Millisecond * 100):
			require.Fail(t, "StartProbes didn't return in time")
		}
	})

	t.Run("should return after closed", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		h := New(config.AppHealthConfig{
			ProbeInterval: time.Second,
		}, func(context.Context) (*Status, error) {
			assert.Fail(t, "unexpected probe call")
			return NewStatus(false, nil), nil
		})
		clock := clocktesting.NewFakeClock(time.Now())
		h.clock = clock

		done := make(chan struct{})
		go func() {
			defer close(done)
			assert.NoError(t, h.StartProbes(ctx))
		}()

		// Wait for ticker to start,
		assert.Eventually(t, clock.HasWaiters, time.Second, time.Microsecond)

		h.Close()

		select {
		case <-done:
		case <-time.After(time.Millisecond * 100):
			require.Fail(t, "StartProbes didn't return in time")
		}
	})

	t.Run("should call app probe function after interval", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		var probeCalls atomic.Int64
		var currentStatus atomic.Bool

		h := New(config.AppHealthConfig{
			ProbeInterval: time.Second,
			Threshold:     1,
		}, func(context.Context) (*Status, error) {
			defer probeCalls.Add(1)
			if probeCalls.Load() == 0 {
				return NewStatus(true, nil), nil
			}
			return NewStatus(false, nil), nil
		})
		clock := clocktesting.NewFakeClock(time.Now())
		h.clock = clock

		done := make(chan struct{})
		go func() {
			defer close(done)
			assert.NoError(t, h.StartProbes(ctx))
		}()

		h.OnHealthChange(func(ctx context.Context, status *Status) {
			currentStatus.Store(status.IsHealthy)
		})

		// Wait for ticker to start,
		assert.Eventually(t, clock.HasWaiters, time.Second, time.Microsecond)
		assert.Equal(t, int64(0), probeCalls.Load())

		clock.Step(time.Second)

		assert.Eventually(t, func() bool {
			return currentStatus.Load() == true
		}, time.Second, time.Microsecond)
		assert.Equal(t, int64(1), probeCalls.Load())

		clock.Step(time.Second)

		assert.Eventually(t, func() bool {
			return currentStatus.Load() == false
		}, time.Second, time.Microsecond)
		assert.Equal(t, int64(2), probeCalls.Load())
		h.Close()

		select {
		case <-done:
		case <-time.After(time.Millisecond * 100):
			require.Fail(t, "StartProbes didn't return in time")
		}
	})
}
