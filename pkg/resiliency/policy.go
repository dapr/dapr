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
	"fmt"
	"io"
	"reflect"
	"sync/atomic"
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

type attemptsCtxKey struct{}

// PolicyDefinition contains a definition for a policy, used to create a Runner.
type PolicyDefinition struct {
	log                       logger.Logger
	name                      string
	t                         time.Duration
	r                         *retry.Config
	cb                        *breaker.CircuitBreaker
	addTimeoutActivatedMetric func()
	addRetryActivatedMetric   func()
	addCBStateChangedMetric   func()
}

// NewPolicyDefinition returns a PolicyDefinition object with the given parameters.
func NewPolicyDefinition(log logger.Logger, name string, t time.Duration, r *retry.Config, cb *breaker.CircuitBreaker) *PolicyDefinition {
	return &PolicyDefinition{
		log:  log,
		name: name,
		t:    t,
		r:    r,
		cb:   cb,
	}
}

// String implements fmt.Stringer and is used for debugging.
func (p PolicyDefinition) String() string {
	return fmt.Sprintf(
		"Policy: name='%s' timeout='%v' retry=(%v) circuitBreaker=(%v)",
		p.name, p.t, p.r, p.cb,
	)
}

// HasRetries returns true if the policy is configured to have more than 1 retry.
func (p PolicyDefinition) HasRetries() bool {
	return p.r != nil && p.r.MaxRetries != 0
}

type RunnerOpts[T any] struct {
	// The disposer is a function which is invoked when the operation fails, including due to timing out in a background goroutine. It receives the value returned by the operation function as long as it's non-zero (e.g. non-nil for pointer types).
	// The disposer can be used to perform cleanup tasks on values returned by the operation function that would otherwise leak (because they're not returned by the result of the runner).
	Disposer func(T)

	// The accumulator is a function that is invoked synchronously when an operation completes without timing out, whether successfully or not. It receives the value returned by the operation function as long as it's non-zero (e.g. non-nil for pointer types).
	// The accumulator can be used to collect intermediate results and not just the final ones, for example in case of working with batched operations.
	Accumulator func(T)
}

// NewRunner returns a policy runner that encapsulates the configured resiliency policies in a simple execution wrapper.
// We can't implement this as a method of the Resiliency struct because we can't yet use generic methods in structs.
func NewRunner[T any](ctx context.Context, def *PolicyDefinition) Runner[T] {
	return NewRunnerWithOptions(ctx, def, RunnerOpts[T]{})
}

// NewRunnerWithOptions is like NewRunner but allows setting additional options
func NewRunnerWithOptions[T any](ctx context.Context, def *PolicyDefinition, opts RunnerOpts[T]) Runner[T] {
	if def == nil {
		return func(oper Operation[T]) (T, error) {
			rRes, rErr := oper(ctx)
			if opts.Accumulator != nil && !isZero(rRes) {
				opts.Accumulator(rRes)
			}
			return rRes, rErr
		}
	}

	var zero T
	timeoutMetricsActivated := atomic.Bool{}
	return func(oper Operation[T]) (T, error) {
		operation := oper
		if def.t > 0 {
			// Handle timeout
			operCopy := operation
			operation = func(ctx context.Context) (T, error) {
				ctx, cancel := context.WithTimeout(ctx, def.t)
				defer cancel()

				done := make(chan doneCh[T], 1)
				go func() {
					rRes, rErr := operCopy(ctx)

					// If the channel is full, it means we had a timeout
					select {
					case done <- doneCh[T]{rRes, rErr}:
						// No timeout, all good
					default:
						// The operation has timed out
						// Invoke the disposer if we have a non-zero return value
						// Note that in case of timeouts we do not invoke the accumulator
						if opts.Disposer != nil && !isZero(rRes) {
							opts.Disposer(rRes)
						}
					}
				}()

				select {
				case v := <-done:
					return v.res, v.err
				case <-ctx.Done():
					// Because done has a capacity of 1, adding a message on the channel signals that there was a timeout
					// However, the response may have arrived in the meanwhile, so we need to also check if something was added
					select {
					case done <- doneCh[T]{}:
						// All good, nothing to do here
					default:
						// The response arrived at the same time as the context deadline, and the channel has a message
						v := <-done

						// Invoke the disposer if the return value is non-zero
						// Note that in case of timeouts we do not invoke the accumulator
						if opts.Disposer != nil && !isZero(v.res) {
							opts.Disposer(v.res)
						}
					}
					if def.addTimeoutActivatedMetric != nil && timeoutMetricsActivated.CompareAndSwap(false, true) {
						def.addTimeoutActivatedMetric()
					}
					return zero, ctx.Err()
				}
			}
		}

		if opts.Accumulator != nil {
			operCopy := operation
			operation = func(ctx context.Context) (T, error) {
				rRes, rErr := operCopy(ctx)
				if !isZero(rRes) {
					opts.Accumulator(rRes)
				}
				return rRes, rErr
			}
		}

		if def.cb != nil {
			operCopy := operation
			operation = func(ctx context.Context) (T, error) {
				prevState := def.cb.State()
				resAny, err := def.cb.Execute(func() (any, error) {
					return operCopy(ctx)
				})
				if def.addCBStateChangedMetric != nil && prevState != def.cb.State() {
					def.addCBStateChangedMetric()
				}
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
		attempts := atomic.Int32{}
		return retry.NotifyRecoverWithData(
			func() (T, error) {
				attempt := attempts.Add(1)
				opCtx := context.WithValue(ctx, attemptsCtxKey{}, attempt)
				rRes, rErr := operation(opCtx)
				// In case of an error, if we have a disposer we invoke it with the return value, then reset the return value
				if rErr != nil && opts.Disposer != nil && !isZero(rRes) {
					opts.Disposer(rRes)
					rRes = zero
				}
				return rRes, rErr
			},
			b,
			func(opErr error, d time.Duration) {
				if def.addRetryActivatedMetric != nil {
					def.addRetryActivatedMetric()
				}
				def.log.Infof("Error processing operation %s. Retrying in %v…", def.name, d)
				def.log.Debugf("Error for operation %s was: %v", def.name, opErr)
			},
			func() {
				def.log.Infof("Recovered processing operation %s after %d attempts", def.name, attempts.Load())
			},
		)
	}
}

// DisposerCloser is a Disposer function for RunnerOpts that invokes Close() on the object.
func DisposerCloser[T io.Closer](obj T) {
	_ = obj.Close()
}

// GetAttempt returns the attempt number from a context
// Attempts are numbered from 1 onwards.
// If the context doesn't have an attempt number, returns 0
func GetAttempt(ctx context.Context) int32 {
	v := ctx.Value(attemptsCtxKey{})
	attempt, ok := v.(int32)
	if !ok {
		return 0
	}
	return attempt
}

func isZero(val any) bool {
	return reflect.ValueOf(val).IsZero()
}
