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

package orchestrator

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	wfengerrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/backend"
)

// cachedETag is the metadata etag a cached instance last saw.
const cachedETag = "etag-1"

// metadataRow encodes a metadata row as the store holds it.
func metadataRow(t *testing.T, generation, historyLength uint64, etag string) *actorapi.StateResponse {
	t.Helper()
	data, err := proto.Marshal(&backend.BackendWorkflowStateMetadata{
		Generation:    generation,
		HistoryLength: historyLength,
	})
	require.NoError(t, err)
	return &actorapi.StateResponse{Data: data, ETag: &etag}
}

func startReminderName(ts time.Time) string {
	return "start-es-" + strconv.Itoa(int(ts.UnixNano()))
}

// A force purge run on another daprd deletes the rows but leaves this host's
// actor, and its cache, resident. Every create verdict the stale cache would
// produce must give way to creating a new instance.
func Test_createWorkflowInstance_purgedOnAnotherHost(t *testing.T) {
	const instanceID = "test-purged-elsewhere"
	oldTS := time.Now().Add(-time.Minute)

	tests := map[string]struct {
		// prime fills the cache with the instance the purge deleted.
		prime func(h *createHarness) *wfenginestate.State
		// incoming builds the create, given its start time.
		incoming func(ts time.Time) *backend.CreateWorkflowInstanceRequest
	}{
		"completed, create enforces uniqueness": {
			prime: func(h *createHarness) *wfenginestate.State {
				return h.primeHistory(startEventFor(instanceID, oldTS, withExecutionID("exec-1")), completedEvent())
			},
			incoming: func(ts time.Time) *backend.CreateWorkflowInstanceRequest {
				return &backend.CreateWorkflowInstanceRequest{
					StartEvent:              startEventFor(instanceID, ts, withExecutionID("exec-2")),
					EnforceUniqueInstanceId: true,
				}
			},
		},
		"completed, parent notification still owed": {
			prime: func(h *createHarness) *wfenginestate.State {
				state := h.primeHistory(startEventFor(instanceID, oldTS, withExecutionID("child-1", parentWithExec("exec-a"))), completedEvent())
				state.SetParentNotifyPending(true)
				return state
			},
			incoming: func(ts time.Time) *backend.CreateWorkflowInstanceRequest {
				return createRequest(startEventFor(instanceID, ts, withExecutionID("exec-2")))
			},
		},
		"completed, activity result awaited": {
			prime: func(h *createHarness) *wfenginestate.State {
				h.orch.activityResultAwaited.Store(true)
				return h.primeHistory(startEventFor(instanceID, oldTS, withExecutionID("exec-1")), completedEvent())
			},
			incoming: func(ts time.Time) *backend.CreateWorkflowInstanceRequest {
				return createRequest(startEventFor(instanceID, ts, withExecutionID("exec-2")))
			},
		},
		"completed, retry of the committed create": {
			prime: func(h *createHarness) *wfenginestate.State {
				return h.primeHistory(startEventFor(instanceID, oldTS, withExecutionID("exec-1")), completedEvent())
			},
			incoming: func(ts time.Time) *backend.CreateWorkflowInstanceRequest {
				return createRequest(startEventFor(instanceID, ts, withExecutionID("exec-1")))
			},
		},
		"running": {
			prime: func(h *createHarness) *wfenginestate.State {
				return h.primeHistory(startEventFor(instanceID, oldTS, withExecutionID("exec-1")))
			},
			incoming: func(ts time.Time) *backend.CreateWorkflowInstanceRequest {
				return createRequest(startEventFor(instanceID, ts, withExecutionID("exec-2")))
			},
		},
		"pending start whose reminder the purge deleted": {
			prime: func(h *createHarness) *wfenginestate.State {
				return h.primePendingStart(startEventFor(instanceID, oldTS, nil))
			},
			incoming: func(ts time.Time) *backend.CreateWorkflowInstanceRequest {
				return createRequest(startEventFor(instanceID, ts, nil))
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			h := newCreateHarness(t, instanceID)
			cached := tc.prime(h)
			etag := cachedETag
			cached.SetMetadataETag(&etag)
			// The purged instance had armed its janitor, which the purge
			// deleted with its other reminders.
			h.orch.janitorAsserted.Store(true)
			h.orch.janitorRedispatched = map[int32]struct{}{0: {}}
			h.storedMeta = func() (*actorapi.StateResponse, error) {
				return &actorapi.StateResponse{}, nil
			}

			ts := time.Now()
			req := tc.incoming(ts)
			require.NoError(t, h.orch.createWorkflowInstance(t.Context(), req))

			h.lock.Lock()
			defer h.lock.Unlock()
			assert.Equal(t, []string{"save", "create:" + startReminderName(ts)}, h.ops,
				"the create must start a new instance, not answer from the purged one")
			require.NotNil(t, h.orch.state)
			assert.NotSame(t, cached, h.orch.state, "the purged instance's state must not be reused")
			assert.Empty(t, h.orch.state.History)
			require.Len(t, h.orch.state.Inbox, 1)
			assert.Same(t, req.GetStartEvent(), h.orch.state.Inbox[0])
			assert.False(t, h.orch.state.ParentNotifyPending)
			assert.False(t, h.orch.activityResultAwaited.Load(), "nothing is owed to a purged instance")
			assert.False(t, h.orch.janitorAsserted.Load(), "the new instance must arm its own janitor")
			assert.Nil(t, h.orch.janitorRedispatched)
		})
	}
}

// The store row still describes the cached instance: the cache is current and
// the create is judged on it.
func Test_createWorkflowInstance_cachedStateConfirmed(t *testing.T) {
	const instanceID = "test-cache-confirmed"

	h := newCreateHarness(t, instanceID)
	cached := h.primeHistory(startEventFor(instanceID, time.Now().Add(-time.Minute), withExecutionID("exec-1")))
	etag := cachedETag
	cached.SetMetadataETag(&etag)
	h.storedMeta = func() (*actorapi.StateResponse, error) {
		return metadataRow(t, cached.Generation, uint64(len(cached.History)), etag), nil
	}

	err := h.orch.createWorkflowInstance(t.Context(), createRequest(startEventFor(instanceID, time.Now(), withExecutionID("exec-2"))))
	require.Error(t, err)
	assert.Equal(t, codes.AlreadyExists, status.Code(err))

	h.lock.Lock()
	defer h.lock.Unlock()
	assert.Empty(t, h.ops)
	assert.Same(t, cached, h.orch.state, "a confirmed cache is kept")
}

// Another host purged the instance and created a new one under the same ID.
// The create must be judged on what the store holds, not on the cache.
func Test_createWorkflowInstance_cacheReloadedWhenStoreChanged(t *testing.T) {
	const instanceID = "test-cache-reloaded"

	h := newCreateHarness(t, instanceID)
	cached := h.primeHistory(startEventFor(instanceID, time.Now().Add(-time.Hour), withExecutionID("exec-1")), completedEvent())
	oldETag := cachedETag
	cached.SetMetadataETag(&oldETag)

	peerStart, err := proto.Marshal(startEventFor(instanceID, time.Now().Add(-time.Minute), withExecutionID("exec-peer")))
	require.NoError(t, err)
	h.storedMeta = func() (*actorapi.StateResponse, error) {
		return metadataRow(t, 1, 1, "etag-2"), nil
	}
	h.storedBulk = actorapi.BulkStateResponse{
		wfenginestate.HistoryKey(0): {Data: peerStart},
	}

	err = h.orch.createWorkflowInstance(t.Context(), createRequest(startEventFor(instanceID, time.Now(), withExecutionID("exec-2"))))
	require.Error(t, err)
	assert.Equal(t, codes.AlreadyExists, status.Code(err),
		"the running instance in the store blocks the create; the completed cache would have recreated over it")

	h.lock.Lock()
	defer h.lock.Unlock()
	assert.Empty(t, h.ops)
	require.NotNil(t, h.orch.state)
	assert.Len(t, h.orch.state.History, 1, "the cache now holds the store's instance")
}

// A row the store has never served an ETag for may be a store that does not
// read its own write yet, not a purge: the cache is kept.
func Test_createWorkflowInstance_missingRowWithoutETagKeepsCache(t *testing.T) {
	const instanceID = "test-cache-no-etag"

	h := newCreateHarness(t, instanceID)
	cached := h.primeHistory(startEventFor(instanceID, time.Now().Add(-time.Minute), withExecutionID("exec-1")))
	h.storedMeta = func() (*actorapi.StateResponse, error) {
		return &actorapi.StateResponse{}, nil
	}

	err := h.orch.createWorkflowInstance(t.Context(), createRequest(startEventFor(instanceID, time.Now(), withExecutionID("exec-2"))))
	require.Error(t, err)
	assert.Equal(t, codes.AlreadyExists, status.Code(err))

	h.lock.Lock()
	defer h.lock.Unlock()
	assert.Empty(t, h.ops)
	assert.Same(t, cached, h.orch.state)
}

// The row cannot be read: the create fails recoverably without acting on a
// cache it could not confirm.
func Test_createWorkflowInstance_metadataReadFailsRecoverably(t *testing.T) {
	const instanceID = "test-cache-read-fails"

	h := newCreateHarness(t, instanceID)
	cached := h.primeHistory(startEventFor(instanceID, time.Now().Add(-time.Minute), withExecutionID("exec-1")), completedEvent())
	etag := cachedETag
	cached.SetMetadataETag(&etag)
	h.storedMeta = func() (*actorapi.StateResponse, error) {
		return nil, errors.New("store unavailable")
	}

	err := h.orch.createWorkflowInstance(t.Context(), createRequest(startEventFor(instanceID, time.Now(), withExecutionID("exec-2"))))
	require.Error(t, err)
	assert.True(t, wfengerrors.IsRecoverable(err))

	h.lock.Lock()
	defer h.lock.Unlock()
	assert.Empty(t, h.ops)
}

// Completions held for the purged instance are not handed to the new one.
func Test_createWorkflowInstance_purgedOnAnotherHost_flushesHeldFolds(t *testing.T) {
	const instanceID = "test-purged-folds"
	h := newCreateHarness(t, instanceID)
	cached := h.primeHistory(startEventFor(instanceID, time.Now().Add(-time.Minute), withExecutionID("exec-1")))
	etag := cachedETag
	cached.SetMetadataETag(&etag)
	h.orch.foldPending = append(h.orch.foldPending, &foldEntry{event: &backend.HistoryEvent{}, gen: cached.Generation, committed: make(chan struct{})})
	h.orch.janitorEscalated = map[int32]*backend.HistoryEvent{0: {}}
	h.storedMeta = func() (*actorapi.StateResponse, error) { return &actorapi.StateResponse{}, nil }

	require.NoError(t, h.orch.createWorkflowInstance(t.Context(), createRequest(startEventFor(instanceID, time.Now(), withExecutionID("exec-2")))))
	assert.Equal(t, cached.Generation, h.orch.state.Generation, "the new instance starts at the purged one's generation")
	assert.Empty(t, h.orch.foldPending)
	assert.Nil(t, h.orch.janitorEscalated)
}
