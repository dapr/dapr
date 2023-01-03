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
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestHealthCheck(t *testing.T) {
	t.Run("unhealthy endpoint custom interval 1, failure threshold 2s", func(t *testing.T) {
		start := time.Now()
		ch := StartEndpointHealthCheck("invalid", WithInterval(time.Second*1), WithFailureThreshold(2))
		for {
			healthy := <-ch
			assert.False(t, healthy)

			d := time.Since(start).Seconds()
			assert.True(t, int(d) < 3)
			return
		}
	})

	t.Run("unhealthy endpoint custom interval 1s, failure threshold 1, initial delay 2s", func(t *testing.T) {
		start := time.Now()
		ch := StartEndpointHealthCheck("invalid", WithInterval(time.Second*1), WithFailureThreshold(1), WithInitialDelay(time.Second*2))
		for {
			healthy := <-ch
			assert.False(t, healthy)

			d := time.Since(start).Seconds()
			assert.True(t, int(d) < 3)
			return
		}
	})

	t.Run("unhealthy endpoint custom interval 1s, failure threshold 2, initial delay 2s", func(t *testing.T) {
		start := time.Now()
		ch := StartEndpointHealthCheck("invalid", WithInterval(time.Second*1), WithFailureThreshold(2), WithInitialDelay(time.Second*2))
		for {
			healthy := <-ch
			assert.False(t, healthy)

			d := time.Since(start).Seconds()
			assert.True(t, int(d) < 4)
			return
		}
	})
}

func TestApplyOptions(t *testing.T) {
	t.Run("valid defaults", func(t *testing.T) {
		opts := healthCheckOptions{}
		applyDefaults(&opts)

		assert.Equal(t, opts.failureThreshold, failureThreshold)
		assert.Equal(t, opts.initialDelay, initialDelay)
		assert.Equal(t, opts.interval, interval)
		assert.Equal(t, opts.requestTimeout, requestTimeout)
		assert.Equal(t, opts.successStatusCode, successStatusCode)
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
		assert.Equal(t, opts.failureThreshold, 10)
		assert.Equal(t, opts.initialDelay, time.Second*11)
		assert.Equal(t, opts.interval, time.Second*12)
		assert.Equal(t, opts.requestTimeout, time.Second*13)
		assert.Equal(t, opts.successStatusCode, 201)
	})
}

type testServer struct {
	statusCode    int
	numberOfCalls atomic.Int64
}

func (t *testServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t.numberOfCalls.Add(1)
	w.WriteHeader(t.statusCode)
	w.Write([]byte(""))
}

func TestResponses(t *testing.T) {
	t.Run("default success status", func(t *testing.T) {
		server := httptest.NewServer(&testServer{
			statusCode: 200,
		})
		defer server.Close()

		ticker := make(chan time.Time)
		ch := StartEndpointHealthCheck(server.URL, WithTicker(ticker), WithFailureThreshold(1))
		for {
			ticker <- time.Now()
			healthy := <-ch
			assert.True(t, healthy)
			return
		}
	})

	t.Run("custom success status", func(t *testing.T) {
		server := httptest.NewServer(&testServer{
			statusCode: 201,
		})
		defer server.Close()

		ticker := make(chan time.Time)
		ch := StartEndpointHealthCheck(server.URL, WithTicker(ticker), WithFailureThreshold(1), WithSuccessStatusCode(201))
		for {
			ticker <- time.Now()
			healthy := <-ch
			assert.True(t, healthy)
			return
		}
	})

	t.Run("test fail", func(t *testing.T) {
		server := httptest.NewServer(&testServer{
			statusCode: 500,
		})
		defer server.Close()

		ticker := make(chan time.Time)
		ch := StartEndpointHealthCheck(server.URL, WithTicker(ticker), WithFailureThreshold(1))
		for {
			ticker <- time.Now()
			healthy := <-ch
			assert.False(t, healthy)
			return
		}
	})

	t.Run("test app recovery", func(t *testing.T) {
		test := &testServer{
			statusCode: 500,
		}
		server := httptest.NewServer(test)
		defer server.Close()

		ticker := make(chan time.Time)
		ch := StartEndpointHealthCheck(server.URL, WithTicker(ticker), WithFailureThreshold(1))
		count := 0
		for {
			ticker <- time.Now()
			healthy := <-ch
			count++
			if count != 2 {
				assert.False(t, healthy)
				test.statusCode = 200
			} else {
				assert.True(t, healthy)

				return
			}
		}
	})
}
