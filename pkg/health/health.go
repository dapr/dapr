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
	"sync/atomic"
	"time"

	"github.com/valyala/fasthttp"
)

const (
	initialDelay      = time.Second * 1
	failureThreshold  = 2
	requestTimeout    = time.Second * 2
	interval          = time.Second * 5
	successStatusCode = 200
)

// Option is a function that applies a health check option.
type Option func(o *healthCheckOptions)

type healthCheckOptions struct {
	initialDelay      time.Duration
	requestTimeout    time.Duration
	failureThreshold  int32
	interval          time.Duration
	successStatusCode int
	ticker            <-chan time.Time
}

// StartEndpointHealthCheck starts a health check on the specified address with the given options.
// It returns a channel that will emit true if the endpoint is healthy and false if the failure conditions
// Have been met.
func StartEndpointHealthCheck(ctx context.Context, endpointAddress string, opts ...Option) chan bool {
	options := &healthCheckOptions{}
	applyDefaults(options)

	for _, o := range opts {
		o(options)
	}
	signalChan := make(chan bool, 1)

	go func(ch chan<- bool, endpointAddress string, options *healthCheckOptions) {
		ticker := options.ticker
		if ticker == nil {
			ticker = time.NewTicker(options.interval).C
		}
		failureCount := &atomic.Int32{}
		time.Sleep(options.initialDelay)

		client := &fasthttp.Client{
			MaxConnsPerHost:           5, // Limit Keep-Alive connections
			ReadTimeout:               options.requestTimeout,
			MaxIdemponentCallAttempts: 1,
		}

		req := fasthttp.AcquireRequest()
		req.SetRequestURI(endpointAddress)
		req.Header.SetMethod(fasthttp.MethodGet)
		defer fasthttp.ReleaseRequest(req)

		for {
			select {
			case <-ticker:
				resp := fasthttp.AcquireResponse()
				err := client.DoTimeout(req, resp, options.requestTimeout)
				if err != nil || resp.StatusCode() != options.successStatusCode {
					if failureCount.Add(1) == options.failureThreshold {
						failureCount.Add(-1)
						select {
						case ch <- false:
							// nop
						default:
							// nop
						}
					}
				} else {
					select {
					case ch <- true:
						// nop
					default:
						// nop
					}
					failureCount.Store(0)
				}
				fasthttp.ReleaseResponse(resp)
			case <-ctx.Done():
				return
			}
		}
	}(signalChan, endpointAddress, options)
	return signalChan
}

func applyDefaults(o *healthCheckOptions) {
	o.failureThreshold = failureThreshold
	o.initialDelay = initialDelay
	o.requestTimeout = requestTimeout
	o.successStatusCode = successStatusCode
	o.interval = interval
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

func WithTicker(ticker <-chan time.Time) Option {
	return func(o *healthCheckOptions) {
		o.ticker = ticker
	}
}
