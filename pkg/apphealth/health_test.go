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
	h.setResult(context.Background(), true)

	statusChange := make(chan uint8, 1)
	unexpectedStatusChanges := atomic.Int32{}
	h.OnHealthChange(func(ctx context.Context, status uint8) {
		select {
		case statusChange <- status:
			// Do nothing
		default:
			// If the channel is full, it means we were not expecting a status change
			unexpectedStatusChanges.Add(1)
		}
	})

	simulateFailures := func(n int32) {
		statusChange <- 255 // Fill the channel
		for i := int32(0); i < n; i++ {
			if i == threshold-1 {
				<-statusChange // Allow the channel to be written into
			}
			h.setResult(context.Background(), false)
			if i == threshold-1 {
				select {
				case v := <-statusChange:
					assert.Equal(t, AppStatusUnhealthy, v)
				case <-time.After(100 * time.Millisecond):
					t.Error("did not get a status change before deadline")
				}
				statusChange <- 255 // Fill the channel again
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
	h.setResult(context.Background(), true)
	select {
	case v := <-statusChange:
		assert.Equal(t, AppStatusHealthy, v)
	case <-time.After(100 * time.Millisecond):
		t.Error("did not get a status change before deadline")
	}
	assert.Equal(t, int32(0), h.failureCount.Load())

	// Multiple invocations in parallel
	// Only one failure should be sent
	wg := sync.WaitGroup{}
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			for i := int32(0); i < (threshold + 5); i++ {
				h.setResult(context.Background(), false)
			}
			wg.Done()
		}()
	}
	wg.Wait()

	select {
	case v := <-statusChange:
		assert.Equal(t, AppStatusUnhealthy, v)
	case <-time.After(50 * time.Millisecond):
		t.Error("did not get a status change before deadline")
	}

	assert.Empty(t, unexpectedStatusChanges.Load())
	assert.Equal(t, (threshold+5)*5, h.failureCount.Load())

	// Test overflows
	h.failureCount.Store(int32(math.MaxInt32 - 2))
	statusChange <- 255 // Fill the channel again
	for i := int32(0); i < 5; i++ {
		h.setResult(context.Background(), false)
	}
	assert.Empty(t, unexpectedStatusChanges.Load())
	assert.Equal(t, threshold+3, h.failureCount.Load())
}

func TestAppHealth_ratelimitReports(t *testing.T) {
	clock := clocktesting.NewFakeClock(time.Now())
	h := New(config.AppHealthConfig{}, nil)
	h.clock = clock

	// First run should always succeed
	require.True(t, h.ratelimitReports())

	// Run again without waiting
	require.False(t, h.ratelimitReports())
	require.False(t, h.ratelimitReports())

	// Step and test
	clock.Step(reportMinInterval)
	require.True(t, h.ratelimitReports())
	require.False(t, h.ratelimitReports())

	// Run tests for 1 second, constantly
	// Should succeed only 10 times.
	clock.Step(reportMinInterval)
	firehose := func(start time.Time, step time.Duration) (passed int64) {
		for clock.Now().Sub(start) < time.Second*10 {
			if h.ratelimitReports() {
				passed++
			}
			clock.Step(step)
		}
		return passed
	}

	passed := firehose(clock.Now(), 10*time.Millisecond)
	assert.Equal(t, int64(10), passed)

	// Repeat, but run with 3 parallel goroutines
	wg := sync.WaitGroup{}
	totalPassed := atomic.Int64{}
	start := clock.Now()
	wg.Add(3)
	for i := 0; i < 3; i++ {
		go func() {
			totalPassed.Add(firehose(start, 3*time.Millisecond))
			wg.Done()
		}()
	}
	wg.Wait()
	passed = totalPassed.Load()
	assert.GreaterOrEqual(t, passed, int64(8))
	assert.LessOrEqual(t, passed, int64(12))
}

func Test_StartProbes(t *testing.T) {
	t.Run("closing context should return", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)

		done := make(chan struct{})

		h := New(config.AppHealthConfig{
			ProbeInterval: time.Second,
		}, func(context.Context) (bool, error) {
			assert.Fail(t, "unexpected probe call")
			return false, nil
		})
		clock := clocktesting.NewFakeClock(time.Now())
		h.clock = clock

		go func() {
			defer close(done)
			require.NoError(t, h.StartProbes(ctx))
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
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)

		h := New(config.AppHealthConfig{
			ProbeInterval: time.Second,
		}, func(context.Context) (bool, error) {
			assert.Fail(t, "unexpected probe call")
			return false, nil
		})

		h.Close()

		done := make(chan struct{})
		go func() {
			defer close(done)
			require.Error(t, h.StartProbes(ctx))
		}()

		select {
		case <-done:
		case <-time.After(time.Millisecond * 100):
			require.Fail(t, "StartProbes didn't return in time")
		}
	})

	t.Run("should return after closed", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)

		h := New(config.AppHealthConfig{
			ProbeInterval: time.Second,
		}, func(context.Context) (bool, error) {
			assert.Fail(t, "unexpected probe call")
			return false, nil
		})
		clock := clocktesting.NewFakeClock(time.Now())
		h.clock = clock

		done := make(chan struct{})
		go func() {
			defer close(done)
			require.NoError(t, h.StartProbes(ctx))
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
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)

		var probeCalls atomic.Int64
		var currentStatus atomic.Uint32

		h := New(config.AppHealthConfig{
			ProbeInterval: time.Second,
			Threshold:     1,
		}, func(context.Context) (bool, error) {
			defer probeCalls.Add(1)
			return probeCalls.Load() == 0, nil
		})
		clock := clocktesting.NewFakeClock(time.Now())
		h.clock = clock

		done := make(chan struct{})
		go func() {
			defer close(done)
			require.NoError(t, h.StartProbes(ctx))
		}()

		h.OnHealthChange(func(ctx context.Context, status uint8) {
			currentStatus.Store(uint32(status))
		})

		// Wait for ticker to start,
		assert.Eventually(t, clock.HasWaiters, time.Second, time.Microsecond)
		assert.Equal(t, int64(0), probeCalls.Load())

		clock.Step(time.Second)

		assert.Eventually(t, func() bool {
			return currentStatus.Load() == uint32(AppStatusHealthy)
		}, time.Second, time.Microsecond)
		assert.Equal(t, int64(1), probeCalls.Load())

		clock.Step(time.Second)

		assert.Eventually(t, func() bool {
			return currentStatus.Load() == uint32(AppStatusUnhealthy)
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
