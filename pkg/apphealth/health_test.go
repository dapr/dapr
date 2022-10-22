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
	"math"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/atomic"
)

func TestAppHealth_setResult(t *testing.T) {
	var threshold int32 = 3
	h := NewAppHealth(&Config{
		Threshold: threshold,
	}, nil)

	// Set the initial state to healthy
	h.setResult(true)

	statusChange := make(chan uint8, 1)
	unexpectedStatusChanges := atomic.NewUint32(0)
	h.OnHealthChange(func(status uint8) {
		select {
		case statusChange <- status:
			// Do nothing
		default:
			// If the channel is full, it means we were not expecting a status change
			unexpectedStatusChanges.Inc()
		}
	})

	simulateFailures := func(n int32) {
		statusChange <- 255 // Fill the channel
		for i := int32(0); i < n; i++ {
			if i == threshold-1 {
				<-statusChange // Allow the channel to be written into
			}
			h.setResult(false)
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
	h.setResult(true)
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
				h.setResult(false)
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
		h.setResult(false)
	}
	assert.Empty(t, unexpectedStatusChanges.Load())
	assert.Equal(t, threshold+3, h.failureCount.Load())
}

func TestAppHealth_ratelimitReports(t *testing.T) {
	// Set to 0.1 seconds
	var minInterval int64 = 1e5

	h := &AppHealth{
		lastReport:        atomic.NewInt64(0),
		reportMinInterval: minInterval,
	}

	// First run should always succeed
	require.True(t, h.ratelimitReports())

	// Run again without waiting
	require.False(t, h.ratelimitReports())
	require.False(t, h.ratelimitReports())

	// Wait and test
	time.Sleep(time.Duration(minInterval+10) * time.Microsecond)
	require.True(t, h.ratelimitReports())
	require.False(t, h.ratelimitReports())

	// Run tests for 1 second, constantly
	// Should succeed only 10 times (+/- 2)
	time.Sleep(time.Duration(minInterval+10) * time.Microsecond)
	firehose := func() (passed int64) {
		var done bool
		deadline := time.After(time.Second)
		for !done {
			select {
			case <-deadline:
				done = true
			default:
				if h.ratelimitReports() {
					passed++
				}
				time.Sleep(10 * time.Nanosecond)
			}
		}

		return passed
	}

	passed := firehose()

	assert.GreaterOrEqual(t, passed, int64(8))
	assert.LessOrEqual(t, passed, int64(12))

	// Repeat, but run with 3 parallel goroutines
	time.Sleep(time.Duration(minInterval+10) * time.Microsecond)
	wg := sync.WaitGroup{}
	totalPassed := atomic.NewInt64(0)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			totalPassed.Add(firehose())
			wg.Done()
		}()
	}
	wg.Wait()

	passed = totalPassed.Load()
	assert.GreaterOrEqual(t, passed, int64(8))
	assert.LessOrEqual(t, passed, int64(12))
}
