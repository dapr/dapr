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

package resiliency_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/resiliency/breaker"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/retry"
)

var log = logger.NewLogger("dapr.resiliency.test")

func TestPolicy(t *testing.T) {
	retryValue := retry.DefaultConfig()
	cbValue := breaker.CircuitBreaker{
		Name:     "test",
		Interval: 10 * time.Millisecond,
		Timeout:  10 * time.Millisecond,
	}
	cbValue.Initialize(log)
	tests := map[string]struct {
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
	}

	ctx := context.Background()
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			called := atomic.Bool{}
			fn := func(ctx context.Context) (any, error) {
				called.Store(true)
				return nil, nil
			}
			policy := resiliency.Policy(ctx, log, name, tt.t, tt.r, tt.cb)
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

			policy := resiliency.Policy(context.Background(), log, "timeout", test.timeout, nil, nil)
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
			expected:   5,
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
			fn := func(ctx context.Context) (any, error) {
				v := called.Add(1)
				if v <= maxCalls {
					return nil, fmt.Errorf("called (%d) vs Max (%d)", v-1, maxCalls)
				}
				return nil, nil
			}

			policy := resiliency.Policy(context.Background(), log, "retry", 10*time.Millisecond, &retry.Config{MaxRetries: test.maxRetries}, nil)
			policy(fn)
			assert.Equal(t, test.expected, called.Load())
		})
	}
}
