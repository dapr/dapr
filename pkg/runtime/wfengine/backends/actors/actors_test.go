/*
Copyright 2026 The Dapr Authors
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

package actors

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	actorsapi "github.com/dapr/dapr/pkg/actors/api"
	actorsfake "github.com/dapr/dapr/pkg/actors/fake"
	actorsstate "github.com/dapr/dapr/pkg/actors/state"
	statefake "github.com/dapr/dapr/pkg/actors/state/fake"
	wfstateerrors "github.com/dapr/dapr/pkg/runtime/wfengine/state/errors"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
)

func TestUniqueEventTimestamp(t *testing.T) {
	t.Run("sequential calls are strictly increasing", func(t *testing.T) {
		abe := &Actors{}
		prev := abe.uniqueEventTimestamp().AsTime().UnixNano()
		for range 1000 {
			next := abe.uniqueEventTimestamp().AsTime().UnixNano()
			assert.Greater(t, next, prev)
			prev = next
		}
	})

	// Concurrent RaiseEvent ingestion must never hand out the same timestamp,
	// otherwise dedup.IsDuplicateExternalEvent would drop a distinct event that
	// happens to race onto the same wall-clock nanosecond.
	t.Run("concurrent calls are all unique", func(t *testing.T) {
		abe := &Actors{}
		const n = 500
		out := make([]int64, n)
		var wg sync.WaitGroup
		wg.Add(n)
		for i := range n {
			go func(i int) {
				defer wg.Done()
				out[i] = abe.uniqueEventTimestamp().AsTime().UnixNano()
			}(i)
		}
		wg.Wait()

		seen := make(map[int64]struct{}, n)
		for _, v := range out {
			_, dup := seen[v]
			require.False(t, dup, "duplicate timestamp issued: %d", v)
			seen[v] = struct{}{}
		}
	})
}

func TestLoadInternalState_RetriesTransientReadError(t *testing.T) {
	t.Parallel()

	// Metadata declares one inbox entry throughout; the bulk read of
	// inbox-000000 fails to observe it for the first failCount calls before
	// "self-healing" and returning it, simulating a concurrent actor write
	// racing the metadata Get and the inbox GetBulk.
	newStore := func(failCount int32) *statefake.Fake {
		metaBytes, err := proto.Marshal(&backend.BackendWorkflowStateMetadata{
			InboxLength: 1,
			Generation:  1,
		})
		require.NoError(t, err)

		eventBytes, err := proto.Marshal(&backend.HistoryEvent{EventId: 0})
		require.NoError(t, err)

		var calls atomic.Int32

		return statefake.New().
			WithGetFn(func(_ context.Context, req *actorsapi.GetStateRequest, _ bool) (*actorsapi.StateResponse, error) {
				return &actorsapi.StateResponse{Data: metaBytes}, nil
			}).
			WithGetBulkFn(func(_ context.Context, req *actorsapi.GetBulkStateRequest, _ bool) (actorsapi.BulkStateResponse, error) {
				out := actorsapi.BulkStateResponse{}
				if calls.Add(1) <= failCount {
					return out, nil
				}
				out["inbox-000000"] = actorsapi.BulkStateEntry{Data: eventBytes}
				return out, nil
			})
	}

	newActors := func(store *statefake.Fake) *Actors {
		return &Actors{
			workflowActorType: "workflow",
			activityActorType: "activity",
			actors: actorsfake.New().WithState(func(context.Context) (actorsstate.Interface, error) {
				return store, nil
			}),
		}
	}

	t.Run("succeeds once the retry budget observes the healed read", func(t *testing.T) {
		t.Parallel()

		abe := newActors(newStore(loadInternalStateMaxRetries))

		wstate, err := abe.loadInternalState(t.Context(), api.InstanceID("wf-1"))
		require.NoError(t, err)
		require.NotNil(t, wstate)
		assert.Len(t, wstate.Inbox, 1)
	})

	t.Run("gives up as a TransientReadError once the retry budget is exhausted", func(t *testing.T) {
		t.Parallel()

		abe := newActors(newStore(loadInternalStateMaxRetries + 1))

		wstate, err := abe.loadInternalState(t.Context(), api.InstanceID("wf-1"))
		require.Error(t, err)
		assert.Nil(t, wstate)

		var transientErr *wfstateerrors.TransientReadError
		require.ErrorAs(t, err, &transientErr)
	})

	t.Run("does not retry a non-transient error", func(t *testing.T) {
		t.Parallel()

		metaBytes, err := proto.Marshal(&backend.BackendWorkflowStateMetadata{
			InboxLength: 1,
			Generation:  1,
		})
		require.NoError(t, err)

		var calls atomic.Int32
		store := statefake.New().
			WithGetFn(func(_ context.Context, req *actorsapi.GetStateRequest, _ bool) (*actorsapi.StateResponse, error) {
				return &actorsapi.StateResponse{Data: metaBytes}, nil
			}).
			WithGetBulkFn(func(_ context.Context, req *actorsapi.GetBulkStateRequest, _ bool) (actorsapi.BulkStateResponse, error) {
				calls.Add(1)
				return nil, errors.New("boom: permanent state store failure")
			})

		abe := newActors(store)

		wstate, err := abe.loadInternalState(t.Context(), api.InstanceID("wf-1"))
		require.Error(t, err)
		assert.Nil(t, wstate)
		assert.Equal(t, int32(1), calls.Load(), "a non-transient error must not be retried")

		var transientErr *wfstateerrors.TransientReadError
		assert.NotErrorAs(t, err, &transientErr)
	})

	t.Run("stops retrying once the context is canceled mid-retry", func(t *testing.T) {
		t.Parallel()

		metaBytes, err := proto.Marshal(&backend.BackendWorkflowStateMetadata{
			InboxLength: 1,
			Generation:  1,
		})
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())
		var calls atomic.Int32
		store := statefake.New().
			WithGetFn(func(_ context.Context, req *actorsapi.GetStateRequest, _ bool) (*actorsapi.StateResponse, error) {
				return &actorsapi.StateResponse{Data: metaBytes}, nil
			}).
			WithGetBulkFn(func(_ context.Context, req *actorsapi.GetBulkStateRequest, _ bool) (actorsapi.BulkStateResponse, error) {
				// Simulate the caller's context being canceled (e.g. the
				// client disconnected) immediately after observing the
				// transient mismatch on the first attempt, before any
				// further retry can run.
				calls.Add(1)
				cancel()
				return actorsapi.BulkStateResponse{}, nil
			})

		abe := newActors(store)

		wstate, err := abe.loadInternalState(ctx, api.InstanceID("wf-1"))
		require.Error(t, err)
		assert.Nil(t, wstate)
		assert.Equal(t, int32(1), calls.Load(), "backoff must stop once the context is canceled instead of exhausting the retry budget")
		assert.ErrorIs(t, err, context.Canceled, "a mid-retry cancellation must surface as context.Canceled, not be masked or retried further")
	})
}
