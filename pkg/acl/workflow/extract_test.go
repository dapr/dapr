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

	"github.com/dapr/durabletask-go/api/protos"
)

func TestParseActorType(t *testing.T) {
	tests := []struct {
		name       string
		actorType  string
		wantOpType OperationType
		wantOK     bool
	}{
		{
			name:       "workflow actor",
			actorType:  "dapr.internal.default.myapp.workflow",
			wantOpType: OperationTypeWorkflow,
			wantOK:     true,
		},
		{
			name:       "activity actor",
			actorType:  "dapr.internal.default.myapp.activity",
			wantOpType: OperationTypeActivity,
			wantOK:     true,
		},
		{
			name:       "executor actor (not workflow/activity)",
			actorType:  "dapr.internal.default.myapp.executor",
			wantOpType: "",
			wantOK:     false,
		},
		{
			name:       "regular user actor",
			actorType:  "MyActor",
			wantOpType: "",
			wantOK:     false,
		},
		{
			name:       "empty string",
			actorType:  "",
			wantOpType: "",
			wantOK:     false,
		},
		{
			name:       "prefix but no suffix",
			actorType:  "dapr.internal.default.myapp",
			wantOpType: "",
			wantOK:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opType, ok := ParseActorType(tt.actorType)
			assert.Equal(t, tt.wantOpType, opType)
			assert.Equal(t, tt.wantOK, ok)
		})
	}
}

func TestExtractOperationName_Workflow(t *testing.T) {
	t.Run("CreateWorkflowInstance extracts workflow name", func(t *testing.T) {
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

		name, subject, err := ExtractOperationName(OperationTypeWorkflow, "CreateWorkflowInstance", data)
		require.NoError(t, err)
		assert.True(t, subject)
		assert.Equal(t, "ProcessOrder", name)
	})

	t.Run("AddWorkflowEvent is not subject to enforcement", func(t *testing.T) {
		name, subject, err := ExtractOperationName(OperationTypeWorkflow, "AddWorkflowEvent", nil)
		require.NoError(t, err)
		assert.False(t, subject)
		assert.Empty(t, name)
	})

	t.Run("PurgeWorkflowState is not subject to enforcement", func(t *testing.T) {
		name, subject, err := ExtractOperationName(OperationTypeWorkflow, "PurgeWorkflowState", nil)
		require.NoError(t, err)
		assert.False(t, subject)
		assert.Empty(t, name)
	})

	t.Run("invalid data returns error", func(t *testing.T) {
		_, _, err := ExtractOperationName(OperationTypeWorkflow, "CreateWorkflowInstance", []byte("invalid"))
		require.Error(t, err)
	})
}

func TestExtractOperationName_Activity(t *testing.T) {
	t.Run("Execute extracts activity name", func(t *testing.T) {
		his := &protos.HistoryEvent{
			EventType: &protos.HistoryEvent_TaskScheduled{
				TaskScheduled: &protos.TaskScheduledEvent{
					Name:  "ChargePayment",
					Input: wrapperspb.String("{}"),
				},
			},
		}
		data, err := proto.Marshal(his)
		require.NoError(t, err)

		name, subject, err := ExtractOperationName(OperationTypeActivity, "Execute", data)
		require.NoError(t, err)
		assert.True(t, subject)
		assert.Equal(t, "ChargePayment", name)
	})

	t.Run("non-Execute method is not subject", func(t *testing.T) {
		name, subject, err := ExtractOperationName(OperationTypeActivity, "SomeOtherMethod", nil)
		require.NoError(t, err)
		assert.False(t, subject)
		assert.Empty(t, name)
	})

	t.Run("invalid data returns error", func(t *testing.T) {
		_, _, err := ExtractOperationName(OperationTypeActivity, "Execute", []byte("invalid"))
		require.Error(t, err)
	})
}

// --- Additional edge case tests ---

func TestParseActorType_NameWithDots(t *testing.T) {
	// Namespace with dots in the actor type string.
	opType, ok := ParseActorType("dapr.internal.my.namespace.myapp.workflow")
	assert.Equal(t, OperationTypeWorkflow, opType)
	assert.True(t, ok)

	opType, ok = ParseActorType("dapr.internal.my.namespace.myapp.activity")
	assert.Equal(t, OperationTypeActivity, opType)
	assert.True(t, ok)
}

func TestParseActorType_JustPrefix(t *testing.T) {
	opType, ok := ParseActorType("dapr.internal.")
	assert.Empty(t, opType)
	assert.False(t, ok)
}

func TestExtractOperationName_WorkflowNilStartEvent(t *testing.T) {
	req := &protos.CreateWorkflowInstanceRequest{
		StartEvent: &protos.HistoryEvent{
			// No ExecutionStarted event set.
		},
	}
	data, err := proto.Marshal(req)
	require.NoError(t, err)

	_, _, err = ExtractOperationName(OperationTypeWorkflow, "CreateWorkflowInstance", data)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ExecutionStarted")
}

func TestExtractOperationName_WorkflowEmptyData(t *testing.T) {
	_, _, err := ExtractOperationName(OperationTypeWorkflow, "CreateWorkflowInstance", []byte{})
	// Empty protobuf unmarshals to zero-value struct, but StartEvent will be nil.
	// The code checks for nil ExecutionStarted and returns an error.
	require.Error(t, err)
}

func TestExtractOperationName_ActivityNilTaskScheduled(t *testing.T) {
	his := &protos.HistoryEvent{
		// No TaskScheduled event set.
	}
	data, err := proto.Marshal(his)
	require.NoError(t, err)

	_, _, err = ExtractOperationName(OperationTypeActivity, "Execute", data)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TaskScheduled")
}

func TestExtractOperationName_ActivityEmptyData(t *testing.T) {
	_, _, err := ExtractOperationName(OperationTypeActivity, "Execute", []byte{})
	// Empty protobuf → nil TaskScheduled → error.
	require.Error(t, err)
}

func TestExtractOperationName_UnknownOpType(t *testing.T) {
	name, subject, err := ExtractOperationName(OperationType("unknown"), "AnyMethod", nil)
	require.NoError(t, err)
	assert.False(t, subject)
	assert.Empty(t, name)
}

func TestExtractOperationName_WorkflowNonSubjectMethods(t *testing.T) {
	// All methods other than CreateWorkflowInstance should not be subject to enforcement.
	for _, method := range []string{"AddWorkflowEvent", "PurgeWorkflowState", "GetWorkflowState", "AnyOtherMethod"} {
		t.Run(method, func(t *testing.T) {
			name, subject, err := ExtractOperationName(OperationTypeWorkflow, method, nil)
			require.NoError(t, err)
			assert.False(t, subject)
			assert.Empty(t, name)
		})
	}
}

func TestExtractOperationName_ActivityNonSubjectMethods(t *testing.T) {
	// All methods other than Execute should not be subject to enforcement.
	for _, method := range []string{"SomeOtherMethod", "Init", "Reminder", ""} {
		t.Run(method, func(t *testing.T) {
			name, subject, err := ExtractOperationName(OperationTypeActivity, method, nil)
			require.NoError(t, err)
			assert.False(t, subject)
			assert.Empty(t, name)
		})
	}
}
