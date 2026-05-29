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

package state

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/pkg/actors/api"
	statefake "github.com/dapr/dapr/pkg/actors/state/fake"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/kit/ptr"
)

func testOpts() Options {
	return Options{
		AppID:             "test-app",
		WorkflowActorType: "workflow",
		ActivityActorType: "activity",
	}
}

func testEvent(id int32) *backend.HistoryEvent {
	return &backend.HistoryEvent{
		EventId:   id,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{},
		},
	}
}

func TestGetPurgeRequest_OmitsCustomStatusWhenNotPersisted(t *testing.T) {
	t.Parallel()

	s := NewState(testOpts())
	s.AddToInbox(testEvent(0))
	s.AddToHistory(testEvent(0))

	req, err := s.GetPurgeRequest("actor1")
	require.NoError(t, err)

	deleteKeys := make(map[string]bool)
	for _, op := range req.Operations {
		if op.Operation == api.Delete {
			if d, ok := op.Request.(api.TransactionalDelete); ok {
				deleteKeys[d.Key] = true
			}
		}
	}

	assert.True(t, deleteKeys["inbox-000000"])
	assert.True(t, deleteKeys["history-000000"])
	assert.True(t, deleteKeys[MetadataKey])
	assert.False(t, deleteKeys[customStatusKey],
		"customStatus delete must be omitted when not persisted")
}

func TestGetPurgeRequest_IncludesCustomStatusWhenPersisted(t *testing.T) {
	t.Parallel()

	s := NewState(testOpts())
	s.AddToInbox(testEvent(0))
	s.AddToHistory(testEvent(0))
	s.CustomStatus = wrapperspb.String("running")
	// Simulate a successful save, which marks customStatus as persisted
	// since a history delta is present.
	s.ResetChangeTracking()

	req, err := s.GetPurgeRequest("actor1")
	require.NoError(t, err)

	deleteKeys := make(map[string]bool)
	for _, op := range req.Operations {
		if op.Operation == api.Delete {
			if d, ok := op.Request.(api.TransactionalDelete); ok {
				deleteKeys[d.Key] = true
			}
		}
	}

	assert.True(t, deleteKeys[customStatusKey])
	assert.True(t, deleteKeys[MetadataKey])
}

func TestResetChangeTracking_TracksCustomStatusPersistence(t *testing.T) {
	t.Parallel()

	t.Run("history change marks customStatus persisted", func(t *testing.T) {
		t.Parallel()
		s := NewState(testOpts())
		s.AddToHistory(testEvent(0))
		assert.False(t, s.customStatusPersisted)
		s.ResetChangeTracking()
		assert.True(t, s.customStatusPersisted)
	})

	t.Run("no history change leaves the flag unchanged", func(t *testing.T) {
		t.Parallel()
		s := NewState(testOpts())
		s.AddToInbox(testEvent(0))
		s.ResetChangeTracking()
		assert.False(t, s.customStatusPersisted)
	})
}

// TestLoadWorkflowState_SetsCustomStatusPersistedFromETag asserts that the
// load-time observation path captures whether customStatus exists in the
// store from the bulk-get ETag, independent of whether the in-memory
// CustomStatus pointer ends up populated (a customStatus row persisted
// with an empty proto loads as a non-nil ETag and a zero-length Data; the
// persistence flag must be set even though CustomStatus stays nil).
func TestLoadWorkflowState_SetsCustomStatusPersistedFromETag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                      string
		customStatusETag          *string
		wantCustomStatusPersisted bool
		wantCustomStatusInPurge   bool
	}{
		{
			name:                      "key absent",
			customStatusETag:          nil,
			wantCustomStatusPersisted: false,
			wantCustomStatusInPurge:   false,
		},
		{
			name:                      "key persisted with empty proto",
			customStatusETag:          ptr.Of("etag-cs"),
			wantCustomStatusPersisted: true,
			wantCustomStatusInPurge:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			const actorID = "wf-load-flag"

			metaBytes, err := proto.Marshal(&backend.WorkflowStateMetadata{
				HistoryLength: 1,
				Generation:    1,
			})
			require.NoError(t, err)
			histBytes, err := proto.Marshal(testEvent(0))
			require.NoError(t, err)

			// Empty Data is the realistic case for customStatus: the
			// runtime upserts an empty StringValue, whose marshalled
			// bytes are zero length, so the ETag is the only signal
			// that the row exists in the store.
			bulk := map[string]api.BulkStateEntry{
				"history-000000": {Data: histBytes},
				customStatusKey: {
					Data: nil,
					ETag: tc.customStatusETag,
				},
			}

			store := statefake.New().
				WithGetFn(func(_ context.Context, req *api.GetStateRequest, _ bool) (*api.StateResponse, error) {
					if req.Key == MetadataKey {
						return &api.StateResponse{Data: metaBytes}, nil
					}
					return &api.StateResponse{}, nil
				}).
				WithGetBulkFn(func(_ context.Context, req *api.GetBulkStateRequest, _ bool) (api.BulkStateResponse, error) {
					out := api.BulkStateResponse{}
					for _, k := range req.Keys {
						out[k] = bulk[k]
					}
					return out, nil
				})

			got, err := LoadWorkflowState(t.Context(), store, actorID, Options{
				WorkflowActorType: "workflow",
				ActivityActorType: "activity",
			})
			require.NoError(t, err)
			require.NotNil(t, got)

			assert.Equal(t, tc.wantCustomStatusPersisted, got.customStatusPersisted,
				"customStatusPersisted")

			req, err := got.GetPurgeRequest(actorID)
			require.NoError(t, err)

			deleteKeys := make(map[string]bool)
			for _, op := range req.Operations {
				if op.Operation == api.Delete {
					if d, ok := op.Request.(api.TransactionalDelete); ok {
						deleteKeys[d.Key] = true
					}
				}
			}

			assert.Equal(t, tc.wantCustomStatusInPurge, deleteKeys[customStatusKey],
				"customStatus delete in purge")
			assert.True(t, deleteKeys[MetadataKey], "metadata delete must always be in purge")
		})
	}
}
