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

package breaker

import (
	"errors"
	"time"

	"github.com/sony/gobreaker"

	"github.com/dapr/dapr/pkg/expr"
	"github.com/dapr/kit/logger"
)

// CircuitBreaker represents the configuration for how
// a circuit breaker behaves.
type CircuitBreaker struct {
	// Name is the circuit breaker name.
	Name string
	// The maximum number of requests allowed to pass through when
	// the circuit breaker is half-open.
	// Default is 1.
	MaxRequests uint32 `mapstructure:"maxRequests"`
	// The cyclic period of the closed state for the circuit breaker
	// to clear the internal counts. If 0, the circuit breaker doesn't
	// clear internal counts during the closed state.
	// Default is 0s.
	Interval time.Duration `mapstructure:"interval"`
	// The period of the open state, after which the state of the
	// circuit breaker becomes half-open.
	// Default is 60s.
	Timeout time.Duration `mapstructure:"timeout"`
	// Trip is a CEL expression evaluated with the circuit breaker's
	// internal counts whenever a request fails in the closed state.
	// If it evaluates to true, the circuit breaker will be placed
	// into the open state.
	// Default is consecutiveFailures > 5.
	Trip *expr.Expr `mapstructure:"trip"`

	breaker *gobreaker.CircuitBreaker
}

var (
	ErrOpenState       = gobreaker.ErrOpenState
	ErrTooManyRequests = gobreaker.ErrTooManyRequests
)

// IsErrorPermanent returns true if `err` should be treated as a
// permanent error that cannot be retried.
func IsErrorPermanent(err error) bool {
	return errors.Is(err, ErrOpenState) || errors.Is(err, ErrTooManyRequests)
}

// Initialize creates the underlying circuit breaker using the
// configuration fields.
func (c *CircuitBreaker) Initialize(log logger.Logger) {
	var tripFn func(counts gobreaker.Counts) bool

	if c.Trip != nil {
		tripFn = func(counts gobreaker.Counts) bool {
			result, err := c.Trip.Eval(map[string]interface{}{
				"requests":             int64(counts.Requests),
				"totalSuccesses":       int64(counts.TotalSuccesses),
				"totalFailures":        int64(counts.TotalFailures),
				"consecutiveSuccesses": int64(counts.ConsecutiveSuccesses),
				"consecutiveFailures":  int64(counts.ConsecutiveFailures),
			})
			if err != nil {
				// We cannot assume it is safe to trip if the eval
				// returns an error.
				return false
			}
			if boolResult, ok := result.(bool); ok {
				return boolResult
			}

			return false
		}
	}

	c.breaker = gobreaker.NewCircuitBreaker(gobreaker.Settings{ // nolint:exhaustivestruct
		Name:        c.Name,
		MaxRequests: c.MaxRequests,
		Interval:    c.Interval,
		Timeout:     c.Timeout,
		ReadyToTrip: tripFn,
		OnStateChange: func(name string, from, to gobreaker.State) {
			log.Infof("Circuit breaker %q changed state from %s to %s", name, from, to)
		},
	})
}

// Execute invokes `oper` if the circuit breaker is in an closed state
// or for an allowed call in the half-open state.
func (c *CircuitBreaker) Execute(oper func() error) error {
	_, err := c.breaker.Execute(func() (interface{}, error) {
		err := oper()

		return nil, err
	})

	// Wrap the error so we don't have to reference the external package in other places.
	switch {
	case errors.Is(err, gobreaker.ErrOpenState):
		return ErrOpenState
	case errors.Is(err, gobreaker.ErrTooManyRequests):
		return ErrTooManyRequests
	default:
		return err //nolint:wrapcheck
	}
}
