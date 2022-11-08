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
	failureThreshold  int
	interval          time.Duration
	successStatusCode int
	ticker            <-chan time.Time
}

// StartEndpointHealthCheck starts a health check on the specified address with the given options.
// It returns a channel that will emit true if the endpoint is healthy and false if the failure conditions
// Have been met.
func StartEndpointHealthCheck(endpointAddress string, opts ...Option) chan bool {
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
		failureCount := 0
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

		for range ticker {
			resp := fasthttp.AcquireResponse()
			err := client.DoTimeout(req, resp, options.requestTimeout)
			if err != nil || resp.StatusCode() != options.successStatusCode {
				failureCount++
				if failureCount == options.failureThreshold {
					failureCount--
					ch <- false
				}
			} else {
				ch <- true
				failureCount = 0
			}
			fasthttp.ReleaseResponse(resp)
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
func WithFailureThreshold(threshold int) Option {
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
