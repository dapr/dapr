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

package workflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

func TestParseActorType(t *testing.T) {
	tests := []struct {
		name       string
		actorType  string
		wantOpType OperationType
		wantOK     bool
	}{
		{"workflow actor", "dapr.internal.default.myapp.workflow", OperationTypeWorkflow, true},
		{"activity actor", "dapr.internal.default.myapp.activity", OperationTypeActivity, true},
		{"executor actor (not workflow/activity)", "dapr.internal.default.myapp.executor", "", false},
		{"regular user actor", "MyActor", "", false},
		{"empty string", "", "", false},
		{"prefix but no suffix", "dapr.internal.default.myapp", "", false},
		{"namespace with dots, workflow", "dapr.internal.my.namespace.myapp.workflow", OperationTypeWorkflow, true},
		{"namespace with dots, activity", "dapr.internal.my.namespace.myapp.activity", OperationTypeActivity, true},
		{"just prefix", "dapr.internal.", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opType, ok := ParseActorType(tt.actorType)
			assert.Equal(t, tt.wantOpType, opType)
			assert.Equal(t, tt.wantOK, ok)
		})
	}
}

func TestWorkflowOperationFromMethod(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		event      *backend.HistoryEvent
		wantOp     wfaclapi.WorkflowOperation
		wantSubj   bool
		wantErr    bool
		errMessage string
	}{
		{
			name:     "CreateWorkflowInstance is schedule",
			method:   "CreateWorkflowInstance",
			wantOp:   wfaclapi.WorkflowOperationSchedule,
			wantSubj: true,
		},
		{
			name:   "AddWorkflowEvent ExecutionTerminated is terminate",
			method: "AddWorkflowEvent",
			event: &backend.HistoryEvent{
				EventType: &protos.HistoryEvent_ExecutionTerminated{
					ExecutionTerminated: &protos.ExecutionTerminatedEvent{},
				},
			},
			wantOp:   wfaclapi.WorkflowOperationTerminate,
			wantSubj: true,
		},
		{
			name:   "AddWorkflowEvent EventRaised is raise",
			method: "AddWorkflowEvent",
			event: &backend.HistoryEvent{
				EventType: &protos.HistoryEvent_EventRaised{
					EventRaised: &protos.EventRaisedEvent{Name: "ev"},
				},
			},
			wantOp:   wfaclapi.WorkflowOperationRaise,
			wantSubj: true,
		},
		{
			name:   "AddWorkflowEvent ExecutionSuspended is pause",
			method: "AddWorkflowEvent",
			event: &backend.HistoryEvent{
				EventType: &protos.HistoryEvent_ExecutionSuspended{
					ExecutionSuspended: &protos.ExecutionSuspendedEvent{},
				},
			},
			wantOp:   wfaclapi.WorkflowOperationPause,
			wantSubj: true,
		},
		{
			name:   "AddWorkflowEvent ExecutionResumed is resume",
			method: "AddWorkflowEvent",
			event: &backend.HistoryEvent{
				EventType: &protos.HistoryEvent_ExecutionResumed{
					ExecutionResumed: &protos.ExecutionResumedEvent{},
				},
			},
			wantOp:   wfaclapi.WorkflowOperationResume,
			wantSubj: true,
		},
		{
			name:     "PurgeWorkflowState is purge",
			method:   "PurgeWorkflowState",
			wantOp:   wfaclapi.WorkflowOperationPurge,
			wantSubj: true,
		},
		{
			name:     "WaitForRuntimeStatus is get",
			method:   "WaitForRuntimeStatus",
			wantOp:   wfaclapi.WorkflowOperationGet,
			wantSubj: true,
		},
		{
			name:     "ForkWorkflowHistory is rerun",
			method:   "ForkWorkflowHistory",
			wantOp:   wfaclapi.WorkflowOperationRerun,
			wantSubj: true,
		},
		{
			name:     "RerunWorkflowInstance is rerun",
			method:   "RerunWorkflowInstance",
			wantOp:   wfaclapi.WorkflowOperationRerun,
			wantSubj: true,
		},
		{
			name:     "unknown method is not subject",
			method:   "SomeInternalMethod",
			wantSubj: false,
		},
		{
			name:       "AddWorkflowEvent without parsed event errors",
			method:     "AddWorkflowEvent",
			event:      nil,
			wantSubj:   true,
			wantErr:    true,
			errMessage: "parsed event is required",
		},
		{
			name:       "AddWorkflowEvent unknown event type errors",
			method:     "AddWorkflowEvent",
			event:      &backend.HistoryEvent{},
			wantSubj:   true,
			wantErr:    true,
			errMessage: "unsupported event type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op, subject, err := WorkflowOperationFromMethod(tt.method, tt.event)
			assert.Equal(t, tt.wantSubj, subject)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errMessage != "" {
					assert.Contains(t, err.Error(), tt.errMessage)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantOp, op)
		})
	}
}

func TestWorkflowNameFromCreateRequest(t *testing.T) {
	t.Run("extracts name from valid request", func(t *testing.T) {
		req := &protos.CreateWorkflowInstanceRequest{
			StartEvent: &protos.HistoryEvent{
				EventType: &protos.HistoryEvent_ExecutionStarted{
					ExecutionStarted: &protos.ExecutionStartedEvent{
						Name: "ProcessOrder",
						WorkflowInstance: &protos.WorkflowInstance{
							InstanceId: "test-123",
						},
					},
				},
			},
		}
		data, err := proto.Marshal(req)
		require.NoError(t, err)

		name, err := WorkflowNameFromCreateRequest(data)
		require.NoError(t, err)
		assert.Equal(t, "ProcessOrder", name)
	})

	t.Run("invalid payload errors", func(t *testing.T) {
		_, err := WorkflowNameFromCreateRequest([]byte("garbage"))
		require.Error(t, err)
	})

	t.Run("missing ExecutionStarted errors", func(t *testing.T) {
		req := &protos.CreateWorkflowInstanceRequest{
			StartEvent: &protos.HistoryEvent{},
		}
		data, err := proto.Marshal(req)
		require.NoError(t, err)

		_, err = WorkflowNameFromCreateRequest(data)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ExecutionStarted")
	})

	t.Run("empty payload errors with missing ExecutionStarted", func(t *testing.T) {
		_, err := WorkflowNameFromCreateRequest([]byte{})
		require.Error(t, err)
	})
}

func TestActivityNameFromExecute(t *testing.T) {
	t.Run("extracts name from Execute", func(t *testing.T) {
		ev := &protos.HistoryEvent{
			EventType: &protos.HistoryEvent_TaskScheduled{
				TaskScheduled: &protos.TaskScheduledEvent{
					Name:  "ChargePayment",
					Input: wrapperspb.String("{}"),
				},
			},
		}
		data, err := proto.Marshal(ev)
		require.NoError(t, err)

		name, subject, err := ActivityNameFromExecute("Execute", data)
		require.NoError(t, err)
		assert.True(t, subject)
		assert.Equal(t, "ChargePayment", name)
	})

	t.Run("non-Execute method is not subject", func(t *testing.T) {
		name, subject, err := ActivityNameFromExecute("Other", nil)
		require.NoError(t, err)
		assert.False(t, subject)
		assert.Empty(t, name)
	})

	t.Run("invalid data errors", func(t *testing.T) {
		_, _, err := ActivityNameFromExecute("Execute", []byte("not a protobuf"))
		require.Error(t, err)
	})

	t.Run("missing TaskScheduled errors", func(t *testing.T) {
		ev := &protos.HistoryEvent{}
		data, err := proto.Marshal(ev)
		require.NoError(t, err)

		_, _, err = ActivityNameFromExecute("Execute", data)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "TaskScheduled")
	})
}
