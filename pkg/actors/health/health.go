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
	"errors"
	"net/http"
	"sync"
	"time"

	"k8s.io/utils/clock"

	"github.com/dapr/kit/logger"
)

const (
	defaultFailureThreshold       = 2
	defaultRequestTimeout         = time.Second * 2
	defaultHealthyStateInterval   = time.Second * 3
	defaultUnHealthyStateInterval = time.Second / 2
	defaultSuccessStatusCode      = http.StatusOK
)

var healthLogger = logger.NewLogger("actorshealth")

type options struct {
	client                 *http.Client
	address                string
	requestTimeout         time.Duration
	failureThreshold       int
	healthyStateInterval   time.Duration
	unhealthyStateInterval time.Duration
	successStatusCode      int
	clock                  clock.WithTicker
}

// Option is a function that applies a health check option.
type Option func(o *options)

// Checker is a health checker which reports to whether the target actor app
// endpoint is in a healthy state, according to the configured status code.
type Checker struct {
	client *http.Client

	ch chan bool

	address                string
	requestTimeout         time.Duration
	failureThreshold       int
	healthyStateInterval   time.Duration
	unhealthyStateInterval time.Duration
	successStatusCode      int

	clock   clock.WithTicker
	closeCh chan struct{}
	once    sync.Once
	wg      sync.WaitGroup
}

func New(opts ...Option) (*Checker, error) {
	options := &options{
		failureThreshold:       defaultFailureThreshold,
		requestTimeout:         defaultRequestTimeout,
		successStatusCode:      defaultSuccessStatusCode,
		healthyStateInterval:   defaultHealthyStateInterval,
		unhealthyStateInterval: defaultUnHealthyStateInterval,
		clock:                  new(clock.RealClock),
	}

	for _, o := range opts {
		o(options)
	}

	if len(options.address) == 0 {
		return nil, errors.New("required option 'address' is missing")
	}

	if options.client == nil {
		options.client = new(http.Client)
	}

	return &Checker{
		ch:                     make(chan bool),
		client:                 options.client,
		address:                options.address,
		requestTimeout:         options.requestTimeout,
		failureThreshold:       options.failureThreshold,
		healthyStateInterval:   options.healthyStateInterval,
		unhealthyStateInterval: options.unhealthyStateInterval,
		successStatusCode:      options.successStatusCode,
		clock:                  options.clock,
		closeCh:                make(chan struct{}),
	}, nil
}

func (c *Checker) Run(ctx context.Context) {
	c.wg.Add(1)
	defer c.Close()
	defer c.wg.Done()

	ctx, cancel := context.WithCancel(ctx)
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		select {
		case <-ctx.Done():
		case <-c.closeCh:
		}
		cancel()
	}()

	for {
		c.doUnHealthyStateCheck(ctx)
		c.doHealthyStateCheck(ctx)

		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}

func (c *Checker) HealthChannel() <-chan bool {
	return c.ch
}

func (c *Checker) Close() {
	defer c.wg.Wait()
	c.once.Do(func() {
		close(c.closeCh)
		c.client.CloseIdleConnections()
		// Wait for health reports to finish before closing the channel.
		c.wg.Wait()
		close(c.ch)
	})
}

func (c *Checker) doUnHealthyStateCheck(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}

	ticker := c.clock.NewTicker(c.unhealthyStateInterval)
	defer ticker.Stop()

	for {
		// Immediately get health status if in unhealthy state.
		if c.getStateHealth(ctx) {
			healthLogger.Info("Actor health check succeeded, marking healthy")
			c.reportHealth(ctx, true)
			return
		}

		select {
		case <-ctx.Done():
			return

		case <-ticker.C():
		}
	}
}

func (c *Checker) doHealthyStateCheck(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}

	ticker := c.clock.NewTicker(c.healthyStateInterval)
	defer ticker.Stop()

	var failureCount int
	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C():
			if c.getStateHealth(ctx) {
				failureCount = 0
			} else {
				failureCount++
			}

			if failureCount >= c.failureThreshold {
				healthLogger.Warnf("Actor health check failed %d times, marking unhealthy", failureCount)
				c.reportHealth(ctx, false)
				return
			}
		}
	}
}

func (c *Checker) reportHealth(ctx context.Context, health bool) {
	select {
	case <-ctx.Done():
	case c.ch <- health:
	}
}

func (c *Checker) getStateHealth(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.address, nil)
	if err != nil {
		healthLogger.Errorf("Error creating request: %s", err)
		return false
	}

	resp, err := c.client.Do(req)
	if err != nil {
		healthLogger.Errorf("Error performing request: %s", err)
		return false
	}
	resp.Body.Close()

	return resp.StatusCode == c.successStatusCode
}

// WithAddress sets the endpoint address for the health check.
func WithAddress(address string) Option {
	return func(o *options) {
		o.address = address
	}
}

// WithFailureThreshold sets the failure threshold for the health check.
func WithFailureThreshold(threshold int) Option {
	return func(o *options) {
		o.failureThreshold = threshold
	}
}

// WithRequestTimeout sets the request timeout for the health check.
func WithRequestTimeout(timeout time.Duration) Option {
	return func(o *options) {
		o.requestTimeout = timeout
	}
}

// WithHTTPClient sets the http.Client to use.
func WithHTTPClient(client *http.Client) Option {
	return func(o *options) {
		o.client = client
	}
}

// WithSuccessStatusCode sets the status code for the health check.
func WithSuccessStatusCode(code int) Option {
	return func(o *options) {
		o.successStatusCode = code
	}
}

// WithHealthyStateInterval sets the interval for the health check.
func WithHealthyStateInterval(interval time.Duration) Option {
	return func(o *options) {
		o.healthyStateInterval = interval
	}
}

// WithUnHealthyStateInterval sets the interval for the health check.
func WithUnHealthyStateInterval(interval time.Duration) Option {
	return func(o *options) {
		o.unhealthyStateInterval = interval
	}
}
