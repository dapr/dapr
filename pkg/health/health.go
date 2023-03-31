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
	"io"
	"net/http"
	"sync/atomic"
	"time"

	kclock "k8s.io/utils/clock"

	"github.com/dapr/kit/logger"
)

const (
	initialDelay      = time.Second * 1
	failureThreshold  = 2
	requestTimeout    = time.Second * 2
	interval          = time.Second * 5
	successStatusCode = http.StatusOK
)

var healthLogger = logger.NewLogger("health")

// Option is a function that applies a health check option.
type Option func(o *healthCheckOptions)

type healthCheckOptions struct {
	initialDelay      time.Duration
	requestTimeout    time.Duration
	failureThreshold  int32
	interval          time.Duration
	client            *http.Client
	successStatusCode int
	clock             kclock.WithTicker
}

// StartEndpointHealthCheck starts a health check on the specified address with the given options.
// It returns a channel that will emit true if the endpoint is healthy and false if the failure conditions
// Have been met.
func StartEndpointHealthCheck(ctx context.Context, endpointAddress string, opts ...Option) <-chan bool {
	options := &healthCheckOptions{}
	applyDefaults(options)

	for _, o := range opts {
		o(options)
	}
	signalChan := make(chan bool, 1)

	ticker := options.clock.NewTicker(options.interval)
	ch := ticker.C()

	go func() {
		failureCount := &atomic.Int32{}

		client := options.client
		if client == nil {
			client = &http.Client{}
		}

		if options.initialDelay > 0 {
			select {
			case <-options.clock.After(options.initialDelay):
			case <-ctx.Done():
			}
		}

		for {
			select {
			case <-ch:
				go doHealthCheck(client, endpointAddress, options.requestTimeout, signalChan, failureCount, options)
			case <-ctx.Done():
				return
			}
		}
	}()
	return signalChan
}

func doHealthCheck(client *http.Client, endpointAddress string, timeout time.Duration, signalChan chan bool, failureCount *atomic.Int32, options *healthCheckOptions) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpointAddress, nil)
	if err != nil {
		healthLogger.Errorf("Failed to create healthcheck request: %v", err)
		return
	}

	res, err := client.Do(req)
	if err != nil || res.StatusCode != options.successStatusCode {
		if failureCount.Add(1) >= options.failureThreshold {
			failureCount.Store(options.failureThreshold - 1)
			signalChan <- false
		}
	} else {
		signalChan <- true
		failureCount.Store(0)
	}
	if res != nil {
		// Drain before closing
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
	}
}

func applyDefaults(o *healthCheckOptions) {
	o.failureThreshold = failureThreshold
	o.initialDelay = initialDelay
	o.requestTimeout = requestTimeout
	o.successStatusCode = successStatusCode
	o.interval = interval
	o.clock = &kclock.RealClock{}
}

// WithInitialDelay sets the initial delay for the health check.
func WithInitialDelay(delay time.Duration) Option {
	return func(o *healthCheckOptions) {
		o.initialDelay = delay
	}
}

// WithFailureThreshold sets the failure threshold for the health check.
func WithFailureThreshold(threshold int32) Option {
	return func(o *healthCheckOptions) {
		o.failureThreshold = threshold
	}
}

// WithRequestTimeout sets the request timeout for the health check.
func WithRequestTimeout(timeout time.Duration) Option {
	return func(o *healthCheckOptions) {
		o.requestTimeout = timeout
	}
}

// WithHTTPClient sets the http.Client to use.
func WithHTTPClient(client *http.Client) Option {
	return func(o *healthCheckOptions) {
		o.client = client
	}
}

// WithSuccessStatusCode sets the status code for the health check.
func WithSuccessStatusCode(code int) Option {
	return func(o *healthCheckOptions) {
		o.successStatusCode = code
	}
}

// WithInterval sets the interval for the health check.
func WithInterval(interval time.Duration) Option {
	return func(o *healthCheckOptions) {
		o.interval = interval
	}
}

// WithClock sets a custom clock (for mocking time).
func WithClock(clock kclock.WithTicker) Option {
	return func(o *healthCheckOptions) {
		o.clock = clock
	}
}
