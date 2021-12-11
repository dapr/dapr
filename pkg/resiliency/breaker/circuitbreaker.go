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
)

type CircuitBreaker struct {
	Name        string
	MaxRequests uint32        `mapstructure:"maxRequests"`
	Interval    time.Duration `mapstructure:"interval"`
	Timeout     time.Duration `mapstructure:"timeout"`
	Trip        *expr.Expr    `mapstructure:"trip"`
	breaker     *gobreaker.CircuitBreaker
}

func IsErrorPermanent(err error) bool {
	return errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests)
}

func (c *CircuitBreaker) Initialize() {
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
				// returns an error
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
	})
}

func (c *CircuitBreaker) Execute(oper func() error) error {
	_, err := c.breaker.Execute(func() (interface{}, error) {
		err := oper()

		return nil, err
	})

	return err // nolint:wrapcheck
}
