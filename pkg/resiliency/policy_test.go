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

package resiliency

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/exp/slices"

	"github.com/dapr/dapr/pkg/resiliency/breaker"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/retry"
)

var testLog = logger.NewLogger("dapr.resiliency.test")

// Example of using NewRunnerWithOptions with an Accumulator function
func ExampleNewRunnerWithOptions_accumulator() {
	// Example polocy definition
	policyDef := &PolicyDefinition{
		log:  testLog,
		name: "retry",
		t:    10 * time.Millisecond,
		r:    &retry.Config{MaxRetries: 6},
	}

	// Handler function
	val := atomic.Int32{}
	fn := func(ctx context.Context) (int32, error) {
		v := val.Add(1)
		// When the value is "2", we add a sleep that will trip the timeout
		// As a consequence, the accumulator is not called, so the value "2" should not be included in the result
		if v == 2 {
			time.Sleep(50 * time.Millisecond)
		}
		// Make this method be executed 4 times in total
		if v <= 3 {
			return v, errors.New("continue")
		}
		return v, nil
	}

	// Invoke the policy and collect all received values
	received := []int32{}
	policy := NewRunnerWithOptions(context.Background(), policyDef, RunnerOpts[int32]{
		Accumulator: func(i int32) {
			// Safe to use non-atomic operations in the next line because "received" is not used in the operation function ("fn")
			received = append(received, i)
		},
	})

	// When using accumulators, the result only contains the last value and is normally ignored
	res, err := policy(fn)

	fmt.Println(res, err, received)
	// Output: 4 <nil> [1 3 4]
}

// Example of using NewRunnerWithOptions with a Disposer function
func ExampleNewRunnerWithOptions_disposer() {
	// Example polocy definition
	policyDef := &PolicyDefinition{
		log:  testLog,
		name: "retry",
		t:    10 * time.Millisecond,
		r:    &retry.Config{MaxRetries: 6},
	}

	// Handler function
	counter := atomic.Int32{}
	fn := func(ctx context.Context) (int32, error) {
		v := counter.Add(1)
		// When the value is "2", we add a sleep that will trip the timeout
		if v == 2 {
			time.Sleep(50 * time.Millisecond)
		}
		if v <= 3 {
			return v, errors.New("continue")
		}
		return v, nil
	}

	// Invoke the policy and collect all disposed values
	disposerCalled := make(chan int32, 5)
	policy := NewRunnerWithOptions(context.Background(), policyDef, RunnerOpts[int32]{
		Disposer: func(val int32) {
			// Dispose the object as needed, for example calling things like:
			// val.Close()

			// Use a buffered channel here because the disposer method should not block
			disposerCalled <- val
		},
	})

	// Execute the policy
	res, err := policy(fn)

	// The disposer should be 3 times called with values 1, 2, 3
	disposed := []int32{}
	for i := 0; i < 3; i++ {
		disposed = append(disposed, <-disposerCalled)
	}
	slices.Sort(disposed)

	fmt.Println(res, err, disposed)
	// Output: 4 <nil> [1 2 3]
}

func TestPolicy(t *testing.T) {
	retryValue := retry.DefaultConfig()
	cbValue := breaker.CircuitBreaker{
		Name:     "test",
		Interval: 10 * time.Millisecond,
		Timeout:  10 * time.Millisecond,
	}
	cbValue.Initialize(testLog)
	tests := map[string]*struct {
		t  time.Duration
		r  *retry.Config
		cb *breaker.CircuitBreaker
	}{
		"empty": {},
		"all": {
			t:  10 * time.Millisecond,
			r:  &retryValue,
			cb: &cbValue,
		},
		"nil policy": nil,
	}

	ctx := context.Background()
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			called := atomic.Bool{}
			fn := func(ctx context.Context) (any, error) {
				called.Store(true)
				return nil, nil
			}
			var policyDef *PolicyDefinition
			if tt != nil {
				policyDef = &PolicyDefinition{
					log:  testLog,
					name: name,
					t:    tt.t,
					r:    tt.r,
					cb:   tt.cb,
				}
			}
			policy := NewRunner[any](ctx, policyDef)
			policy(fn)
			assert.True(t, called.Load())
		})
	}
}

func TestPolicyTimeout(t *testing.T) {
	tests := []struct {
		name      string
		sleepTime time.Duration
		timeout   time.Duration
		expected  bool
	}{
		{
			name:      "Timeout expires",
			sleepTime: time.Millisecond * 100,
			timeout:   time.Millisecond * 10,
			expected:  false,
		},
		{
			name:      "Timeout OK",
			sleepTime: time.Millisecond * 10,
			timeout:   time.Millisecond * 100,
			expected:  true,
		},
	}

	for i := range tests {
		test := tests[i]
		t.Run(test.name, func(t *testing.T) {
			called := atomic.Bool{}
			fn := func(ctx context.Context) (any, error) {
				time.Sleep(test.sleepTime)
				called.Store(true)
				return nil, nil
			}

			policy := NewRunner[any](context.Background(), &PolicyDefinition{
				log:  testLog,
				name: "timeout",
				t:    test.timeout,
			})
			policy(fn)

			assert.Equal(t, test.expected, called.Load())
		})
	}
}

func TestPolicyRetry(t *testing.T) {
	tests := []struct {
		name       string
		maxCalls   int32
		maxRetries int64
		expected   int32
	}{
		{
			name:       "Retries succeed",
			maxCalls:   5,
			maxRetries: 6,
			expected:   6,
		},
		{
			name:       "Retries fail",
			maxCalls:   5,
			maxRetries: 2,
			expected:   3,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := atomic.Int32{}
			maxCalls := test.maxCalls
			fn := func(ctx context.Context) (struct{}, error) {
				v := called.Add(1)
				attempt := GetAttempt(ctx)
				if attempt != v {
					return struct{}{}, backoff.Permanent(fmt.Errorf("expected attempt in context to be %d but got %d", v, attempt))
				}
				if v <= maxCalls {
					return struct{}{}, fmt.Errorf("called (%d) vs Max (%d)", v-1, maxCalls)
				}
				return struct{}{}, nil
			}

			policy := NewRunner[struct{}](context.Background(), &PolicyDefinition{
				log:  testLog,
				name: "retry",
				t:    10 * time.Millisecond,
				r:    &retry.Config{MaxRetries: test.maxRetries},
			})
			_, err := policy(fn)
			if err != nil {
				assert.NotContains(t, err.Error(), "expected attempt in context to be")
			}
			assert.Equal(t, test.expected, called.Load())
		})
	}
}

func TestPolicyAccumulator(t *testing.T) {
	val := atomic.Int32{}
	fnCalled := atomic.Int32{}
	fn := func(ctx context.Context) (int32, error) {
		fnCalled.Add(1)
		v := val.Load()
		if v == 0 || v == 2 {
			// Because the accumulator isn't called when "val" is 0 or 2, we need to increment "val" here
			val.Add(1)
		}
		// When the value is "2", we add a sleep that will trip the timeout
		// As a consequence, the accumulator is not called, so the value "2" should not be included in the result
		if v == 2 {
			time.Sleep(50 * time.Millisecond)
		}
		if v <= 3 {
			return v, errors.New("continue")
		}
		return v, nil
	}

	received := []int32{}
	policyDef := &PolicyDefinition{
		log:  testLog,
		name: "retry",
		t:    10 * time.Millisecond,
		r:    &retry.Config{MaxRetries: 6},
	}
	var accumulatorCalled int
	policy := NewRunnerWithOptions(context.Background(), policyDef, RunnerOpts[int32]{
		Accumulator: func(i int32) {
			// Only reason for incrementing "val" here is to have something to check for race conditions with "go test -race"
			val.Add(1)
			// Safe to use non-atomic operations in the next line because "received" is not used in the operation function ("fn")
			received = append(received, i)
			accumulatorCalled++
		},
	})
	res, err := policy(fn)

	// Sleep a bit to ensure that things in the background aren't continuing
	time.Sleep(100 * time.Millisecond)

	require.NoError(t, err)
	// res should contain only the last result, i.e. 4
	assert.Equal(t, int32(4), res)
	assert.Equal(t, 3, accumulatorCalled)
	assert.Equal(t, int32(5), fnCalled.Load())
	assert.Equal(t, []int32{1, 3, 4}, received)
}

func TestPolicyDisposer(t *testing.T) {
	counter := atomic.Int32{}
	fn := func(ctx context.Context) (int32, error) {
		v := counter.Add(1)
		// When the value is "2", we add a sleep that will trip the timeout
		if v == 2 {
			time.Sleep(50 * time.Millisecond)
		}
		if v <= 3 {
			return v, errors.New("continue")
		}
		return v, nil
	}

	disposerCalled := make(chan int32, 5)
	policyDef := &PolicyDefinition{
		log:  testLog,
		name: "retry",
		t:    10 * time.Millisecond,
		r:    &retry.Config{MaxRetries: 5},
	}
	policy := NewRunnerWithOptions(context.Background(), policyDef, RunnerOpts[int32]{
		Disposer: func(i int32) {
			disposerCalled <- i
		},
	})
	res, err := policy(fn)

	require.NoError(t, err)
	// res should contain only the last result, i.e. 4
	assert.Equal(t, int32(4), res)

	// The disposer should be 3 times called with values 1, 2, 3
	disposed := []int32{}
	for i := 0; i < 3; i++ {
		disposed = append(disposed, <-disposerCalled)
	}
	// Shouldn't have more messages coming in
	select {
	case <-time.After(100 * time.Millisecond):
		// all good
	case <-disposerCalled:
		t.Error("received an extra message we were not expecting")
	}
	slices.Sort(disposed)
	assert.Equal(t, []int32{1, 2, 3}, disposed)
}
