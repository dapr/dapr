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

func TestApplyOptions(t *testing.T) {
	t.Run("no address should error", func(t *testing.T) {
		checker, err := New()
		require.Error(t, err)
		assert.Nil(t, checker)
	})

	t.Run("valid defaults", func(t *testing.T) {
		checker, err := New(
			WithAddress("http://localhost:8080"),
		)
		require.NoError(t, err)

		assert.Equal(t, defaultFailureThreshold, 2)
		assert.Equal(t, defaultHealthyStateInterval, time.Second*3)
		assert.Equal(t, defaultUnHealthyStateInterval, time.Second/2)
		assert.Equal(t, defaultRequestTimeout, time.Second*2)
		assert.Equal(t, defaultSuccessStatusCode, 200)

		assert.Equal(t, "http://localhost:8080", checker.address)
		assert.Equal(t, defaultFailureThreshold, checker.failureThreshold)
		assert.Equal(t, defaultHealthyStateInterval, checker.healthyStateInterval)
		assert.Equal(t, defaultUnHealthyStateInterval, checker.unhealthyStateInterval)
		assert.Equal(t, defaultRequestTimeout, checker.requestTimeout)
		assert.Equal(t, defaultSuccessStatusCode, checker.successStatusCode)
	})

	t.Run("valid custom options", func(t *testing.T) {
		checker, err := New(
			WithAddress("http://localhost:8081"),
			WithFailureThreshold(10),
			WithHealthyStateInterval(time.Second*12),
			WithUnHealthyStateInterval(time.Second*15),
			WithRequestTimeout(time.Second*13),
			WithSuccessStatusCode(201),
		)
		require.NoError(t, err)

		assert.Equal(t, "http://localhost:8081", checker.address)
		assert.Equal(t, 10, checker.failureThreshold)
		assert.Equal(t, time.Second*12, checker.healthyStateInterval)
		assert.Equal(t, time.Second*15, checker.unhealthyStateInterval)
		assert.Equal(t, time.Second*13, checker.requestTimeout)
		assert.Equal(t, 201, checker.successStatusCode)
	})
}

func TestResponses(t *testing.T) {
	t.Run("default success status", func(t *testing.T) {
		ch, clock, ts := testChecker(t,
			200,
			WithFailureThreshold(2),
		)

		clock.Step(1)
		assert.True(t, assertHealthSignal(t, clock, ch))

		assert.Equal(t, int64(1), ts.numberOfCalls.Load())

		ts.statusCode.Store(500)

		assert.Eventually(t, clock.HasWaiters, time.Second, time.Millisecond*10)
		clock.Step(time.Second * 3)
		assertNoHealthSignal(t, clock, ch)
		assert.Equal(t, int64(2), ts.numberOfCalls.Load())

		assert.Eventually(t, clock.HasWaiters, time.Second, time.Millisecond*10)
		clock.Step(time.Second * 3)
		assert.False(t, assertHealthSignal(t, clock, ch))
		assert.Equal(t, int64(3), ts.numberOfCalls.Load())
	})

	t.Run("custom success status", func(t *testing.T) {
		ch, clock, ts := testChecker(t,
			201,
			WithFailureThreshold(1),
			WithSuccessStatusCode(201),
		)

		clock.Step(1)
		assert.True(t, assertHealthSignal(t, clock, ch))
		assert.Equal(t, int64(1), ts.numberOfCalls.Load())

		ts.statusCode.Store(200)
		assert.Eventually(t, clock.HasWaiters, time.Second, time.Millisecond*10)
		clock.Step(time.Second * 3)
		assert.False(t, assertHealthSignal(t, clock, ch))
		assert.Equal(t, int64(2), ts.numberOfCalls.Load())
	})

	t.Run("test app recovery", func(t *testing.T) {
		ch, clock, ts := testChecker(t,
			200,
			WithFailureThreshold(1),
		)

		clock.Step(1)
		assert.True(t, assertHealthSignal(t, clock, ch))
		assert.Equal(t, int64(1), ts.numberOfCalls.Load())

		ts.statusCode.Store(300)
		assert.Eventually(t, clock.HasWaiters, time.Second, time.Millisecond*10)
		clock.Step(time.Second * 3)
		assert.False(t, assertHealthSignal(t, clock, ch))
		assert.Equal(t, int64(2), ts.numberOfCalls.Load())

		ts.statusCode.Store(200)
		assert.Eventually(t, clock.HasWaiters, time.Second, time.Millisecond*10)
		clock.Step(time.Second / 2)
		assert.True(t, assertHealthSignal(t, clock, ch))
		assert.Equal(t, int64(3), ts.numberOfCalls.Load())
	})
}

func testChecker(t *testing.T, initialCode int64, opts ...Option) (<-chan bool, *clocktesting.FakeClock, *testServer) {
	ts := new(testServer)
	ts.statusCode.Store(initialCode)
	server := httptest.NewServer(ts)
	t.Cleanup(server.Close)

	clock := clocktesting.NewFakeClock(time.Now())

	checker, err := New(append(opts,
		WithAddress(server.URL),
		WithClock(clock),
	)...)
	require.NoError(t, err)

	doneCh := make(chan error)
	t.Cleanup(func() {
		checker.Close()
		select {
		case <-doneCh:
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for checker to stop")
		}
	})

	go func() {
		checker.Run(context.Background())
		close(doneCh)
	}()

	require.Eventually(t, clock.HasWaiters, time.Second, time.Millisecond*10)

	return checker.HealthChannel(), clock, ts
}

func assertHealthSignal(t *testing.T, clock *clocktesting.FakeClock, ch <-chan bool) bool {
	t.Helper()

	// Wait to ensure ticker in health server is setup.
	// Wait for the clock to have tickers before stepping, since they are likely
	// being created in another go routine to this test.
	require.Eventually(t, clock.HasWaiters, time.Second, time.Millisecond*10, "ticker in program not created in time")

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
	require.Eventually(t, clock.HasWaiters, time.Second, time.Millisecond*10, "ticker in program not created in time")

	// The signal is sent in a background goroutine, so we need to use a wall clock here
	select {
	case <-ch:
		t.Fatal("received unexpected signal")
	case <-time.After(200 * time.Millisecond):
		// all good
	}
}

type testServer struct {
	statusCode    atomic.Int64
	numberOfCalls atomic.Int64
}

func (t *testServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t.numberOfCalls.Add(1)
	w.WriteHeader(int(t.statusCode.Load()))
}

func (t *testServer) SetStatusCode(statusCode int64) {
	t.statusCode.Store(statusCode)
}
