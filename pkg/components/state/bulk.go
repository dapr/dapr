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

package state

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/cenkalti/backoff/v4"
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/resiliency"
)

// PerformBulkStore performs a bulk set or delete using resiliency, retrying operations that fail only when they can be retried.
func PerformBulkStore[T state.SetRequest | state.DeleteRequest](
	ctx context.Context, reqs []T, policyDef *resiliency.PolicyDefinition,
	execSingle func(ctx context.Context, req *T) error,
	execMulti func(ctx context.Context, reqs []T) error,
) error {
	var reqsAtomic atomic.Pointer[[]T]
	reqsAtomic.Store(&reqs)
	policyRunner := resiliency.NewRunnerWithOptions(ctx,
		policyDef,
		resiliency.RunnerOpts[[]int]{
			// In case of errors, the policy runner function returns a list of items that are to be retried.
			// Items that can NOT be retried are the items that either succeeded (when at least one other item failed) or which failed with an etag error (which can't be retried)
			Accumulator: func(retry []int) {
				// This function is executed synchronously so we can modify reqs
				rReqs := *reqsAtomic.Load()
				newReqs := make([]T, len(retry))
				for i, seq := range retry {
					newReqs[i] = rReqs[seq]
				}
				reqsAtomic.Store(&newReqs)
			},
		},
	)
	_, err := policyRunner(func(ctx context.Context) ([]int, error) {
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
		rErr = execMulti(ctx, rReqs)

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
		retry := make([]int, 0, len(errs))
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
				retry = append(retry, bse.Sequence())
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
