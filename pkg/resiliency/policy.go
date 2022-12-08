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

package resiliency

import (
	"context"
	"errors"
	"time"

	"github.com/cenkalti/backoff/v4"

	"github.com/dapr/dapr/pkg/resiliency/breaker"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/retry"
)

type (
	// Operation represents a function to invoke with resiliency policies applied.
	Operation[T any] func(ctx context.Context) (T, error)

	// Runner represents a function to invoke `oper` with resiliency policies applied.
	Runner[T any] func(oper Operation[T]) (T, error)
)

type doneCh[T any] struct {
	res T
	err error
}

// PolicyDefinition contains a definition for a policy, used to create a Runner.
type PolicyDefinition struct {
	log  logger.Logger
	name string
	t    time.Duration
	r    *retry.Config
	cb   *breaker.CircuitBreaker
}

// NewRunner returns a policy runner that encapsulates the configured resiliency policies in a simple execution wrapper.
// We can't implement this as a method of the Resiliency struct because we can't yet use generic methods in structs.
func NewRunner[T any](ctx context.Context, def *PolicyDefinition) Runner[T] {
	if def == nil {
		return func(oper Operation[T]) (T, error) {
			return oper(ctx)
		}
	}

	var zero T
	return func(oper Operation[T]) (T, error) {
		operation := oper
		if def.t > 0 {
			// Handle timeout
			// TODO: This should ideally be handled by the underlying service/component. Revisit once those understand contexts
			operCopy := operation
			operation = func(ctx context.Context) (T, error) {
				ctx, cancel := context.WithTimeout(ctx, def.t)
				defer cancel()

				done := make(chan doneCh[T])
				go func() {
					rRes, rErr := operCopy(ctx)
					done <- doneCh[T]{rRes, rErr}
				}()

				select {
				case v := <-done:
					return v.res, v.err
				case <-ctx.Done():
					return zero, ctx.Err()
				}
			}
		}

		if def.cb != nil {
			operCopy := operation
			operation = func(ctx context.Context) (T, error) {
				resAny, err := def.cb.Execute(func() (any, error) {
					return operCopy(ctx)
				})
				if def.r != nil && breaker.IsErrorPermanent(err) {
					// Break out of retry
					err = backoff.Permanent(err)
				}
				res, ok := resAny.(T)
				if !ok && resAny != nil {
					err = errors.New("cannot cast response to specific type")
					if def.r != nil {
						// Break out of retry
						err = backoff.Permanent(err)
					}
				}
				return res, err
			}
		}

		if def.r == nil {
			return operation(ctx)
		}

		// Use retry/back off
		b := def.r.NewBackOffWithContext(ctx)
		return retry.NotifyRecoverWithData(
			func() (T, error) {
				return operation(ctx)
			},
			b,
			func(opErr error, _ time.Duration) {
				def.log.Infof("Error processing operation %s. Retrying…", def.name)
				def.log.Debugf("Error for operation %s was: %v", def.name, opErr)
			},
			func() {
				def.log.Infof("Recovered processing operation %s.", def.name)
			},
		)
	}
}
