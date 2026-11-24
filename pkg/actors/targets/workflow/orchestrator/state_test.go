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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

// The cached metadata, which the remote GetInstance path and the status
// streams send, has a completion time only for a completed instance, and the
// creation time of a PENDING instance from its start event in the inbox.
func TestPrimeCachedStateTimes(t *testing.T) {
	created := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
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

	prime := func(t *testing.T, inbox, history []*backend.HistoryEvent) *backend.WorkflowMetadata {
		t.Helper()
		state := wfenginestate.NewState(wfenginestate.Options{})
		for _, e := range inbox {
			state.AddToInbox(e)
		}
		for _, e := range history {
			state.AddToHistory(e)
		}
		o := &orchestrator{actorID: "wf-1"}
		o.primeCachedState(state, o.getExecutionStartedEvent(state))
		require.NotNil(t, o.ometa)
		return o.ometa
	}

	t.Run("completed", func(t *testing.T) {
		meta := prime(t, nil, []*backend.HistoryEvent{wfStarted, start, completed})
		assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
		assert.Equal(t, at(0).AsTime(), meta.GetCreatedAt().AsTime())
		require.NotNil(t, meta.GetCompletedAt())
		assert.Equal(t, at(3).AsTime(), meta.GetCompletedAt().AsTime())
	})

	t.Run("running", func(t *testing.T) {
		meta := prime(t, nil, []*backend.HistoryEvent{wfStarted, start})
		assert.Equal(t, api.RUNTIME_STATUS_RUNNING, meta.GetRuntimeStatus())
		assert.Equal(t, at(0).AsTime(), meta.GetCreatedAt().AsTime())
		assert.Nil(t, meta.GetCompletedAt())
	})

	t.Run("pending", func(t *testing.T) {
		meta := prime(t, []*backend.HistoryEvent{start}, nil)
		assert.Equal(t, api.RUNTIME_STATUS_PENDING, meta.GetRuntimeStatus())
		assert.Equal(t, "wf", meta.GetName())
		assert.Equal(t, at(0).AsTime(), meta.GetCreatedAt().AsTime())
		assert.Nil(t, meta.GetCompletedAt())
	})

	t.Run("a new state without a start event", func(t *testing.T) {
		meta := prime(t, nil, nil)
		assert.Nil(t, meta.GetCompletedAt())
	})
}
