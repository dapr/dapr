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

package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clocktesting "k8s.io/utils/clock/testing"
)

var startOfTime = time.Date(2022, 1, 1, 12, 0, 0, 0, time.UTC)

func TestHealthCheck(t *testing.T) {
	ts := &testServer{}
	server := httptest.NewServer(ts)
	defer server.Close()

	t.Run("unhealthy endpoint, custom interval 1, failure threshold 2s", func(t *testing.T) {
		ts.SetStatusCode(200)

		clock := &clocktesting.FakeClock{}
		clock.SetTime(startOfTime)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ch := StartEndpointHealthCheck(ctx,
			WithAddress(server.URL),
			WithClock(clock),
			WithInterval(time.Second),
			WithFailureThreshold(2),
			WithInitialDelay(0),
		)

		// First healthcheck is always unsuccessful, right away
		healthy := assertHealthSignal(t, clock, ch)
		assert.False(t, healthy)

		// First actual healthcheck is successful
		clock.Step(time.Second)
		healthy = assertHealthSignal(t, clock, ch)
		assert.True(t, healthy)

		// Set to unsuccessful
		ts.SetStatusCode(500)

		// Nothing happens for the first second
		clock.Step(time.Second)
		assertNoHealthSignal(t, clock, ch)

		// Get a signal after the next tick
		clock.Step(time.Second)
		healthy = assertHealthSignal(t, clock, ch)
		assert.False(t, healthy)
	})

	t.Run("healthy endpoint, custom interval 1s, failure threshold 1, initial delay 2s", func(t *testing.T) {
		ts.SetStatusCode(200)

		clock := &clocktesting.FakeClock{}
		clock.SetTime(startOfTime)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ch := StartEndpointHealthCheck(ctx,
			WithAddress(server.URL),
			WithClock(clock),
			WithInterval(time.Second),
			WithFailureThreshold(1),
			WithInitialDelay(time.Second*2),
		)

		// Nothing happens for the first 2s
		for i := 0; i < 2; i++ {
			clock.Step(time.Second)
			assertNoHealthSignal(t, clock, ch)
		}

		// Get a signal right away
		clock.Step(time.Second)
		healthy := assertHealthSignal(t, clock, ch)
		assert.False(t, healthy)

		// App recovers after the next tick
		clock.Step(time.Second)
		healthy = assertHealthSignal(t, clock, ch)
		assert.True(t, healthy)
	})

	t.Run("unhealthy endpoint, custom interval 1s, failure threshold 2, initial delay 2s", func(t *testing.T) {
		ts.SetStatusCode(200)

		clock := &clocktesting.FakeClock{}
		clock.SetTime(startOfTime)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ch := StartEndpointHealthCheck(ctx,
			WithAddress(server.URL),
			WithClock(clock),
			WithInterval(time.Second*1),
			WithFailureThreshold(2),
			WithInitialDelay(time.Second*2),
		)

		// Nothing happens for the first 2s
		for i := 0; i < 2; i++ {
			clock.Step(time.Second)
			assertNoHealthSignal(t, clock, ch)
		}

		// Get a signal right away
		clock.Step(time.Second)
		healthy := assertHealthSignal(t, clock, ch)
		assert.False(t, healthy)

		// App recovers after the next tick
		clock.Step(time.Second)
		healthy = assertHealthSignal(t, clock, ch)
		assert.True(t, healthy)

		// Set to unsuccessful
		ts.SetStatusCode(500)

		// Nothing happens for the first 1s
		clock.Step(time.Second)
		assertNoHealthSignal(t, clock, ch)

		// Get a signal after the next tick
		clock.Step(time.Second)
		healthy = assertHealthSignal(t, clock, ch)
		assert.False(t, healthy)
	})
}

func TestApplyOptions(t *testing.T) {
	t.Run("valid defaults", func(t *testing.T) {
		opts := healthCheckOptions{}
		applyDefaults(&opts)

		assert.Equal(t, int32(failureThreshold), opts.failureThreshold)
		assert.Equal(t, initialDelay, opts.initialDelay)
		assert.Equal(t, interval, opts.interval)
		assert.Equal(t, requestTimeout, opts.requestTimeout)
		assert.Equal(t, successStatusCode, opts.successStatusCode)
	})

	t.Run("valid custom options", func(t *testing.T) {
		opts := healthCheckOptions{}
		applyDefaults(&opts)

		customOpts := []Option{
			WithFailureThreshold(10),
			WithInitialDelay(time.Second * 11),
			WithInterval(time.Second * 12),
			WithRequestTimeout(time.Second * 13),
			WithSuccessStatusCode(201),
		}
		for _, o := range customOpts {
			o(&opts)
		}
		assert.Equal(t, int32(10), opts.failureThreshold)
		assert.Equal(t, time.Second*11, opts.initialDelay)
		assert.Equal(t, time.Second*12, opts.interval)
		assert.Equal(t, time.Second*13, opts.requestTimeout)
		assert.Equal(t, 201, opts.successStatusCode)
	})
}

type testServer struct {
	statusCode    atomic.Int32
	numberOfCalls atomic.Int64
}

func (t *testServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t.numberOfCalls.Add(1)
	w.WriteHeader(int(t.statusCode.Load()))
}

func (t *testServer) SetStatusCode(statusCode int32) {
	t.statusCode.Store(statusCode)
}

func TestResponses(t *testing.T) {
	t.Run("default success status", func(t *testing.T) {
		clock := &clocktesting.FakeClock{}
		clock.SetTime(startOfTime)

		ts := &testServer{}
		ts.SetStatusCode(200)
		server := httptest.NewServer(ts)
		defer server.Close()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ch := StartEndpointHealthCheck(ctx,
			WithAddress(server.URL),
			WithClock(clock),
			WithInitialDelay(0),
			WithFailureThreshold(1),
		)

		healthy := assertHealthSignal(t, clock, ch)
		assert.False(t, healthy)

		clock.Step(5 * time.Second)
		healthy = assertHealthSignal(t, clock, ch)
		assert.True(t, healthy)
	})

	t.Run("custom success status", func(t *testing.T) {
		clock := &clocktesting.FakeClock{}
		clock.SetTime(startOfTime)

		ts := &testServer{}
		ts.SetStatusCode(201)
		server := httptest.NewServer(ts)
		defer server.Close()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ch := StartEndpointHealthCheck(ctx,
			WithAddress(server.URL),
			WithClock(clock),
			WithInitialDelay(0),
			WithFailureThreshold(1),
			WithSuccessStatusCode(201),
		)

		healthy := assertHealthSignal(t, clock, ch)
		assert.False(t, healthy)

		clock.Step(5 * time.Second)
		healthy = assertHealthSignal(t, clock, ch)
		assert.True(t, healthy)
	})

	t.Run("test fail", func(t *testing.T) {
		clock := &clocktesting.FakeClock{}
		clock.SetTime(startOfTime)

		ts := &testServer{}
		ts.SetStatusCode(500)
		server := httptest.NewServer(ts)
		defer server.Close()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ch := StartEndpointHealthCheck(ctx,
			WithAddress(server.URL),
			WithClock(clock),
			WithInitialDelay(0),
			WithFailureThreshold(1),
		)
		healthy := assertHealthSignal(t, clock, ch)
		assert.False(t, healthy)

		clock.Step(5 * time.Second)
		assertNoHealthSignal(t, clock, ch)
	})

	t.Run("test app recovery", func(t *testing.T) {
		clock := &clocktesting.FakeClock{}
		clock.SetTime(startOfTime)

		ts := &testServer{}
		ts.SetStatusCode(500)
		server := httptest.NewServer(ts)
		defer server.Close()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ch := StartEndpointHealthCheck(ctx,
			WithAddress(server.URL),
			WithClock(clock),
			WithInitialDelay(0),
			WithFailureThreshold(1),
		)

		healthy := assertHealthSignal(t, clock, ch)
		assert.False(t, healthy)

		for i := 0; i <= 1; i++ {
			clock.Step(5 * time.Second)
			if i == 0 {
				assertNoHealthSignal(t, clock, ch)
				ts.SetStatusCode(200)
			} else {
				healthy = assertHealthSignal(t, clock, ch)
				assert.True(t, healthy)
			}
		}
	})
}

func assertHealthSignal(t *testing.T, clock *clocktesting.FakeClock, ch <-chan bool) bool {
	t.Helper()
	// Wait to ensure ticker in health server is setup.
	// Wait for the clock to have tickers before stepping, since they are likely
	// being created in another go routine to this test.
	require.Eventually(t, func() bool {
		return clock.HasWaiters()
	}, time.Second, time.Microsecond, "ticker in program not created in time")

	select {
	case v := <-ch:
		return v
	case <-time.After(200 * time.Millisecond):
		t.Fatal("did not receive a signal in 200ms")
	}
	return false
}

func assertNoHealthSignal(t *testing.T, clock *clocktesting.FakeClock, ch <-chan bool) {
	t.Helper()

	// Wait to ensure ticker in health server is setup.
	// Wait for the clock to have tickers before stepping, since they are likely
	// being created in another go routine to this test.
	require.Eventually(t, func() bool {
		return clock.HasWaiters()
	}, time.Second, time.Microsecond, "ticker in program not created in time")

	// The signal is sent in a background goroutine, so we need to use a wall clock here
	select {
	case <-ch:
		t.Fatal("received unexpected signal")
	case <-time.After(200 * time.Millisecond):
		// all good
	}
}
