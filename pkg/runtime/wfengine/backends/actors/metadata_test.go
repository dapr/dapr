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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	actorsapi "github.com/dapr/dapr/pkg/actors/api"
	actorsfake "github.com/dapr/dapr/pkg/actors/fake"
	actorstate "github.com/dapr/dapr/pkg/actors/state"
	statefake "github.com/dapr/dapr/pkg/actors/state/fake"
	wfstate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/kit/ptr"
)

// GetWorkflowMetadata gives a completion time only for a completed instance,
// and the creation time of a PENDING instance from its start event in the
// inbox.
func TestGetWorkflowMetadataTimes(t *testing.T) {
	const (
		appID     = "myapp"
		actorType = "dapr.internal.default." + appID + ".workflow"
	)

	// A recent creation time, so the pending start is not overdue.
	created := time.Now().UTC().Truncate(time.Second)
	at := func(sec int) *timestamppb.Timestamp {
		return timestamppb.New(created.Add(time.Duration(sec) * time.Second))
	}
	start := &backend.HistoryEvent{EventId: -1, Timestamp: at(0), EventType: &protos.HistoryEvent_ExecutionStarted{
		ExecutionStarted: &protos.ExecutionStartedEvent{Name: "wf"},
	}}
	wfStarted := &backend.HistoryEvent{EventId: -1, Timestamp: at(1), EventType: &protos.HistoryEvent_WorkflowStarted{
		WorkflowStarted: &protos.WorkflowStartedEvent{},
	}}
	completed := &backend.HistoryEvent{EventId: -1, Timestamp: at(3), EventType: &protos.HistoryEvent_ExecutionCompleted{
		ExecutionCompleted: &protos.ExecutionCompletedEvent{WorkflowStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED},
	}}

	// metadata saves an instance with the inbox and history given, and
	// returns what GetWorkflowMetadata gives for it.
	metadata := func(t *testing.T, inbox, history []*backend.HistoryEvent) *backend.WorkflowMetadata {
		t.Helper()
		opts := wfstate.Options{AppID: appID, Namespace: "default", WorkflowActorType: actorType}
		ws := wfstate.NewState(opts)
		for _, e := range inbox {
			ws.AddToInbox(e)
		}
		for _, e := range history {
			ws.AddToHistory(e)
		}
		req, err := ws.GetSaveRequest("wf-1")
		require.NoError(t, err)
		rows := make(map[string][]byte)
		for _, op := range req.Operations {
			if u, ok := op.Request.(actorsapi.TransactionalUpsert); ok {
				b, ok := u.Value.([]byte)
				require.True(t, ok)
				rows[u.Key] = b
			}
		}

		sf := statefake.New().
			WithGetFn(func(_ context.Context, req *actorsapi.GetStateRequest, _ bool) (*actorsapi.StateResponse, error) {
				if b, ok := rows[req.Key]; ok {
					return &actorsapi.StateResponse{Data: b, ETag: ptr.Of("1")}, nil
				}
				return &actorsapi.StateResponse{}, nil
			}).
			WithGetBulkFn(func(_ context.Context, req *actorsapi.GetBulkStateRequest, _ bool) (actorsapi.BulkStateResponse, error) {
				out := make(actorsapi.BulkStateResponse, len(req.Keys))
				for _, k := range req.Keys {
					if b, ok := rows[k]; ok {
						out[k] = actorsapi.BulkStateEntry{Data: b, ETag: ptr.Of("1")}
					}
				}
				return out, nil
			})
		abe := &Actors{
			appID:             appID,
			namespace:         "default",
			workflowActorType: actorType,
			actors: actorsfake.New().WithState(func(context.Context) (actorstate.Interface, error) {
				return sf, nil
			}),
		}

		meta, err := abe.GetWorkflowMetadata(t.Context(), "wf-1", nil)
		require.NoError(t, err)
		return meta
	}

	t.Run("completed", func(t *testing.T) {
		meta := metadata(t, nil, []*backend.HistoryEvent{wfStarted, start, completed})
		assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
		assert.Equal(t, at(0).AsTime(), meta.GetCreatedAt().AsTime())
		require.NotNil(t, meta.GetCompletedAt())
		assert.Equal(t, at(3).AsTime(), meta.GetCompletedAt().AsTime())
	})

	t.Run("running", func(t *testing.T) {
		meta := metadata(t, nil, []*backend.HistoryEvent{wfStarted, start})
		assert.Equal(t, api.RUNTIME_STATUS_RUNNING, meta.GetRuntimeStatus())
		assert.Equal(t, at(0).AsTime(), meta.GetCreatedAt().AsTime())
		assert.Nil(t, meta.GetCompletedAt())
	})

	t.Run("pending", func(t *testing.T) {
		meta := metadata(t, []*backend.HistoryEvent{start}, nil)
		assert.Equal(t, api.RUNTIME_STATUS_PENDING, meta.GetRuntimeStatus())
		assert.Equal(t, at(0).AsTime(), meta.GetCreatedAt().AsTime())
		assert.Nil(t, meta.GetCompletedAt())
	})
}
