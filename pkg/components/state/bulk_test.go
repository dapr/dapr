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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/components-contrib/state"
	resiliencyV1alpha1 "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
)

func TestPerformBulkStoreOperation(t *testing.T) {
	simulatedErr := errors.New("simulated")
	etagMismatchErr := state.NewETagError(state.ETagMismatch, simulatedErr)
	etagInvalidErr := state.NewETagError(state.ETagInvalid, simulatedErr)

	res := resiliency.FromConfigurations(logger.NewLogger("test"), &resiliencyV1alpha1.Resiliency{
		Spec: resiliencyV1alpha1.ResiliencySpec{
			Policies: resiliencyV1alpha1.Policies{
				Retries: map[string]resiliencyV1alpha1.Retry{
					"singleRetry": {
						MaxRetries:  ptr.Of(1),
						MaxInterval: "100ms",
						Policy:      "constant",
						Duration:    "10ms",
					},
				},
				Timeouts: map[string]string{
					"fast": "100ms",
				},
			},
			Targets: resiliencyV1alpha1.Targets{
				Components: map[string]resiliencyV1alpha1.ComponentPolicyNames{
					"mystate": {
						Outbound: resiliencyV1alpha1.PolicyNames{
							Retry:   "singleRetry",
							Timeout: "fast",
						},
					},
				},
			},
		},
	})
	policyDef := res.ComponentOutboundPolicy("mystate", resiliency.Statestore)

	t.Run("single request", func(t *testing.T) {
		reqs := []state.SetRequest{
			{Key: "key1"},
		}

		t.Run("no error", func(t *testing.T) {
			count := atomic.Uint32{}
			err := PerformBulkStoreOperation(context.Background(), reqs, policyDef, state.BulkStoreOpts{},
				func(ctx context.Context, req *state.SetRequest) error {
					count.Add(1)
					return nil
				},
				nil, // The multi method should not be invoked, so this will panic if it happens
			)
			require.NoError(t, err)
			require.Equal(t, uint32(1), count.Load())
		})

		t.Run("does not retry on etag error", func(t *testing.T) {
			count := atomic.Uint32{}
			err := PerformBulkStoreOperation(context.Background(), reqs, policyDef, state.BulkStoreOpts{},
				func(ctx context.Context, req *state.SetRequest) error {
					count.Add(1)
					return etagInvalidErr
				},
				nil, // The multi method should not be invoked, so this will panic if it happens
			)
			var etagErr *state.ETagError
			require.Error(t, err)
			require.ErrorAs(t, err, &etagErr)
			require.Equal(t, uint32(1), count.Load())
		})

		t.Run("retries on other errors", func(t *testing.T) {
			count := atomic.Uint32{}
			err := PerformBulkStoreOperation(context.Background(), reqs, policyDef, state.BulkStoreOpts{},
				func(ctx context.Context, req *state.SetRequest) error {
					count.Add(1)
					return simulatedErr
				},
				nil, // The multi method should not be invoked, so this will panic if it happens
			)
			require.Error(t, err)
			require.Equal(t, uint32(2), count.Load())
		})

		t.Run("success on second attempt", func(t *testing.T) {
			count := atomic.Uint32{}
			err := PerformBulkStoreOperation(context.Background(), reqs, policyDef, state.BulkStoreOpts{},
				func(ctx context.Context, req *state.SetRequest) error {
					if count.Add(1) == 1 {
						return simulatedErr
					}
					return nil
				},
				nil, // The multi method should not be invoked, so this will panic if it happens
			)
			require.NoError(t, err)
			require.Equal(t, uint32(2), count.Load())
		})
	})

	t.Run("multiple requests", func(t *testing.T) {
		reqs := []state.SetRequest{
			{Key: "key1"},
			{Key: "key2"},
		}

		t.Run("all successful", func(t *testing.T) {
			count := atomic.Uint32{}
			err := PerformBulkStoreOperation(context.Background(), reqs, policyDef, state.BulkStoreOpts{},
				nil, // The single method should not be invoked, so this will panic if it happens
				func(ctx context.Context, req []state.SetRequest, opts state.BulkStoreOpts) error {
					count.Add(1)
					return nil
				},
			)
			require.NoError(t, err)
			require.Equal(t, uint32(1), count.Load())
		})

		t.Run("key1 successful, key2 etag mismatch", func(t *testing.T) {
			count := atomic.Uint32{}
			err := PerformBulkStoreOperation(context.Background(), reqs, policyDef, state.BulkStoreOpts{},
				nil, // The single method should not be invoked, so this will panic if it happens
				func(ctx context.Context, req []state.SetRequest, opts state.BulkStoreOpts) error {
					count.Add(1)
					return errors.Join(
						state.NewBulkStoreError("key2", etagMismatchErr),
					)
				},
			)
			require.Error(t, err)
			var etagErr *state.ETagError
			require.ErrorAs(t, err, &etagErr)
			require.Equal(t, state.ETagMismatch, etagErr.Kind())
			require.Equal(t, uint32(1), count.Load())
		})

		t.Run("key1 etag invalid, key2 etag mismatch", func(t *testing.T) {
			count := atomic.Uint32{}
			err := PerformBulkStoreOperation(context.Background(), reqs, policyDef, state.BulkStoreOpts{},
				nil, // The single method should not be invoked, so this will panic if it happens
				func(ctx context.Context, req []state.SetRequest, opts state.BulkStoreOpts) error {
					count.Add(1)
					return errors.Join(
						state.NewBulkStoreError("key1", etagInvalidErr),
						state.NewBulkStoreError("key2", etagMismatchErr),
					)
				},
			)
			require.Error(t, err)
			var etagErr *state.ETagError
			require.ErrorAs(t, err, &etagErr)
			require.Equal(t, state.ETagInvalid, etagErr.Kind())
			require.Equal(t, uint32(1), count.Load())
		})

		t.Run("key1 successful, key2 fails and is retried", func(t *testing.T) {
			count := atomic.Uint32{}
			// This should retry, but the second time only key2 should be requested
			err := PerformBulkStoreOperation(context.Background(), reqs, policyDef, state.BulkStoreOpts{},
				func(ctx context.Context, req *state.SetRequest) error {
					require.Equal(t, "key2", req.Key)
					count.Add(1)
					return simulatedErr
				},
				func(ctx context.Context, req []state.SetRequest, opts state.BulkStoreOpts) error {
					count.Add(1)
					return errors.Join(
						state.NewBulkStoreError("key2", simulatedErr),
					)
				},
			)
			require.Error(t, err)
			require.Equal(t, simulatedErr, err)
			require.Equal(t, uint32(2), count.Load())
		})

		t.Run("key1 fails and is retried, key2 has etag error", func(t *testing.T) {
			count := atomic.Uint32{}
			// This should retry, but the second time only key1 should be requested
			err := PerformBulkStoreOperation(context.Background(), reqs, policyDef, state.BulkStoreOpts{},
				func(ctx context.Context, req *state.SetRequest) error {
					require.Equal(t, "key1", req.Key)
					count.Add(1)
					return simulatedErr
				},
				func(ctx context.Context, req []state.SetRequest, opts state.BulkStoreOpts) error {
					count.Add(1)
					return errors.Join(
						state.NewBulkStoreError("key1", simulatedErr),
						state.NewBulkStoreError("key2", etagMismatchErr),
					)
				},
			)
			require.Error(t, err)
			require.Equal(t, simulatedErr, err)
			require.Equal(t, uint32(2), count.Load())
		})

		t.Run("key1 fails and is retried, key2 has etag error, key3 succeeds", func(t *testing.T) {
			reqs2 := []state.SetRequest{
				{Key: "key1"},
				{Key: "key2"},
				{Key: "key3"},
			}

			count := atomic.Uint32{}
			// This should retry, but the second time only key1 should be requested
			err := PerformBulkStoreOperation(context.Background(), reqs2, policyDef, state.BulkStoreOpts{},
				func(ctx context.Context, req *state.SetRequest) error {
					require.Equal(t, "key1", req.Key)
					count.Add(1)
					return simulatedErr
				},
				func(ctx context.Context, req []state.SetRequest, opts state.BulkStoreOpts) error {
					count.Add(1)
					return errors.Join(
						state.NewBulkStoreError("key1", simulatedErr),
						state.NewBulkStoreError("key2", etagMismatchErr),
					)
				},
			)
			require.Error(t, err)
			require.Equal(t, simulatedErr, err)
			require.Equal(t, uint32(2), count.Load())
		})

		t.Run("key1 succeeds on 2nd attempt, key2 succeeds, key3 has etag error on 2nd attempt", func(t *testing.T) {
			reqs2 := []state.SetRequest{
				{Key: "key1"},
				{Key: "key2"},
				{Key: "key3"},
			}

			count := atomic.Uint32{}
			err := PerformBulkStoreOperation(context.Background(), reqs2, policyDef, state.BulkStoreOpts{},
				nil, // The single method should not be invoked, so this will panic if it happens
				func(ctx context.Context, req []state.SetRequest, opts state.BulkStoreOpts) error {
					if count.Add(1) == 1 {
						// On first attempt, key1 and key3 fail with non-etag errors
						return errors.Join(
							state.NewBulkStoreError("key1", simulatedErr),
							state.NewBulkStoreError("key3", simulatedErr),
						)
					}

					// On the second attempt, key3 fails with etag error
					require.Len(t, req, 2)
					for i := 0; i < 2; i++ {
						switch req[i].Key {
						case "key3", "key1":
							// All good
						default:
							t.Fatalf("Found unexpected key: %s", req[i].Key)
						}
					}
					return errors.Join(
						state.NewBulkStoreError("key3", etagMismatchErr),
					)
				},
			)
			require.Error(t, err)
			var etagErr *state.ETagError
			require.ErrorAs(t, err, &etagErr)
			require.Equal(t, state.ETagMismatch, etagErr.Kind())
			require.Equal(t, uint32(2), count.Load())
		})

		t.Run("retries when error is not a multierror", func(t *testing.T) {
			count := atomic.Uint32{}
			err := PerformBulkStoreOperation(context.Background(), reqs, policyDef, state.BulkStoreOpts{},
				nil, // The single method should not be invoked, so this will panic if it happens
				func(ctx context.Context, req []state.SetRequest, opts state.BulkStoreOpts) error {
					count.Add(1)
					return simulatedErr
				},
			)
			require.Error(t, err)
			require.Equal(t, simulatedErr, err)
			require.Equal(t, uint32(2), count.Load())
		})

		t.Run("retries when multierror contains a non-BulkStoreError error", func(t *testing.T) {
			count := atomic.Uint32{}
			err := PerformBulkStoreOperation(context.Background(), reqs, policyDef, state.BulkStoreOpts{},
				nil, // The single method should not be invoked, so this will panic if it happens
				func(ctx context.Context, req []state.SetRequest, opts state.BulkStoreOpts) error {
					count.Add(1)
					return errors.Join(simulatedErr)
				},
			)
			require.Error(t, err)
			merr, ok := err.(interface{ Unwrap() []error })
			require.True(t, ok)
			require.Len(t, merr.Unwrap(), 1)
			require.Equal(t, merr.Unwrap()[0], simulatedErr)
			require.Equal(t, uint32(2), count.Load())
		})
	})
}
