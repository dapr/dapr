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

package state

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/cenkalti/backoff/v4"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/resiliency"
)

type stateRequestConstraint interface {
	state.SetRequest | state.DeleteRequest
	state.StateRequest
}

func requestWithKey[T stateRequestConstraint](reqs []T, key string) int {
	for i, r := range reqs {
		if r.GetKey() == key {
			return i
		}
	}

	// Should never happen…
	return -1
}

// PerformBulkStoreOperation performs a bulk set or delete using resiliency, retrying operations that fail only when they can be retried.
func PerformBulkStoreOperation[T stateRequestConstraint](
	ctx context.Context, reqs []T, policyDef *resiliency.PolicyDefinition, opts state.BulkStoreOpts,
	execSingle func(ctx context.Context, req *T) error,
	execMulti func(ctx context.Context, reqs []T, opts state.BulkStoreOpts) error,
) error {
	var reqsAtomic atomic.Pointer[[]T]
	reqsAtomic.Store(&reqs)
	policyRunner := resiliency.NewRunnerWithOptions(ctx,
		policyDef,
		resiliency.RunnerOpts[[]string]{
			// In case of errors, the policy runner function returns a list of items that are to be retried.
			// Items that can NOT be retried are the items that either succeeded (when at least one other item failed) or which failed with an etag error (which can't be retried)
			Accumulator: func(retry []string) {
				rReqs := *reqsAtomic.Load()
				newReqs := make([]T, len(retry))
				var n int
				for _, retryKey := range retry {
					i := requestWithKey(reqs, retryKey)
					if i >= 0 {
						newReqs[n] = rReqs[i]
						n++
					}
				}
				newReqs = newReqs[:n]
				reqsAtomic.Store(&newReqs)
			},
		},
	)
	_, err := policyRunner(func(ctx context.Context) ([]string, error) {
		var rErr error
		rReqs := *reqsAtomic.Load()

		// If there's a single request, perform it in non-bulk
		// In this case, we never need to filter out operations
		if len(rReqs) == 1 {
			rErr = execSingle(ctx, &rReqs[0])
			if rErr != nil {
				// Check if it's an etag error, which is not retriable
				// In that case, wrap inside a permanent backoff error
				var etagErr *state.ETagError
				if errors.As(rErr, &etagErr) {
					rErr = backoff.Permanent(rErr)
				}
			}
			return nil, rErr
		}

		// Perform the request in bulk
		rErr = execMulti(ctx, rReqs, opts)

		// If there's no error, short-circuit
		if rErr == nil {
			return nil, nil
		}

		// Check if we have a multi-error; if not, return the error as-is
		mErr, ok := rErr.(interface{ Unwrap() []error })
		if !ok {
			return nil, rErr
		}
		errs := mErr.Unwrap()
		if len(errs) == 0 {
			// Should never happen…
			return nil, rErr
		}

		// Check which operation(s) failed
		// We can retry if at least one error is not an etag error
		var canRetry, etagInvalid bool
		retry := make([]string, 0, len(errs))
		for _, e := range errs {
			// Check if it's a BulkStoreError
			// If not, we will cause all operations to be retried, because the error was not a multi BulkStoreError
			var bse state.BulkStoreError
			if !errors.As(e, &bse) {
				return nil, rErr
			}

			// If it's not an etag error, the operation can retry this failed item
			if etagErr := bse.ETagError(); etagErr == nil {
				canRetry = true
				retry = append(retry, bse.Key())
			} else if etagErr.Kind() == state.ETagInvalid {
				// If at least one etag error is due to an etag invalid, record that
				etagInvalid = true
			}
		}

		// If canRetry is false, it means that all errors are etag errors, which are permanent
		if !canRetry {
			var etagErr *state.ETagError
			if etagInvalid {
				etagErr = state.NewETagError(state.ETagInvalid, rErr)
			} else {
				etagErr = state.NewETagError(state.ETagMismatch, rErr)
			}
			rErr = backoff.Permanent(etagErr)
		}
		return retry, rErr
	})

	return err
}
