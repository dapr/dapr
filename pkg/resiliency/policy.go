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
	"time"

	"github.com/cenkalti/backoff/v4"

	"github.com/dapr/dapr/pkg/resiliency/breaker"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/retry"
)

type (
	// Operation represents a function to invoke with resiliency policies applied.
	Operation func(ctx context.Context) error

	// Runner represents a function to invoke `oper` with resiliency policies applied.
	Runner func(oper Operation) error
)

// Policy returns a policy runner that encapsulates the configured
// resiliency policies in a simple execution wrapper.
func Policy(ctx context.Context, log logger.Logger, operationName string, t time.Duration, r *retry.Config, cb *breaker.CircuitBreaker) Runner {
	return func(oper Operation) error {
		operation := oper
		if t > 0 {
			// Handle timeout.
			// TODO: This should ideally be handled by the underlying service/component. Revisit once those understand contexts.
			operCopy := operation
			operation = func(ctx context.Context) error {
				ctx, cancel := context.WithTimeout(ctx, t)
				defer cancel()

				done := make(chan error)
				go func() {
					done <- operCopy(ctx)
				}()

				select {
				case err := <-done:
					return err
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}

		if cb != nil {
			operCopy := operation
			operation = func(ctx context.Context) error {
				err := cb.Execute(func() error {
					return operCopy(ctx)
				})
				if r != nil && breaker.IsErrorPermanent(err) {
					// Break out of retry.
					err = backoff.Permanent(err)
				}
				return err
			}
		}

		if r == nil {
			return operation(ctx)
		}

		// Use retry/back off.
		b := r.NewBackOffWithContext(ctx)
		return retry.NotifyRecover(func() error {
			return operation(ctx)
		}, b, func(_ error, _ time.Duration) {
			log.Infof("Error processing operation %s. Retrying...", operationName)
		}, func() {
			log.Infof("Recovered processing operation %s.", operationName)
		})
	}
}
