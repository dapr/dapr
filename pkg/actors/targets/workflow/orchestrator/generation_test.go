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

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"

	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

// generationState is a persisted state whose history starts with an
// ExecutionStarted carrying prevExecID, as the parent's state looks when a
// ContinueAsNew turn runs.
func generationState(prevExecID string, generation uint64) *wfenginestate.State {
	state := wfenginestate.NewState(wfenginestate.Options{
		AppID:             "testapp",
		Namespace:         "default",
		WorkflowActorType: "dapr.internal.default.testapp.workflow",
		ActivityActorType: "dapr.internal.default.testapp.activity",
	})
	var execID *wrapperspb.StringValue
	if prevExecID != "" {
		execID = wrapperspb.String(prevExecID)
	}
	state.History = []*backend.HistoryEvent{{
		EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{
				Name:             "parent",
				WorkflowInstance: &protos.WorkflowInstance{InstanceId: "wf", ExecutionId: execID},
			},
		},
	}}
	state.Generation = generation
	return state
}

// canTurnState is what the engine hands back after a ContinueAsNew turn
// that also created two children: the new generation's ExecutionStarted with
// a freshly minted random id, and a pending create per child carrying that
// same id as its parent.
func canTurnState() *backend.WorkflowRuntimeState {
	minted := wrapperspb.String(uuid.NewString())
	child := func(id string) *backend.WorkflowRuntimeStateMessage {
		return &backend.WorkflowRuntimeStateMessage{
			TargetInstanceId: id,
			HistoryEvent: &backend.HistoryEvent{
				EventType: &protos.HistoryEvent_ExecutionStarted{
					ExecutionStarted: &protos.ExecutionStartedEvent{
						Name:             "child",
						WorkflowInstance: &protos.WorkflowInstance{InstanceId: id, ExecutionId: wrapperspb.String(uuid.NewString())},
						ParentInstance: &protos.ParentInstanceInfo{
							TaskScheduledId:  0,
							WorkflowInstance: &protos.WorkflowInstance{InstanceId: "wf", ExecutionId: minted},
						},
					},
				},
			},
		}
	}
	return &backend.WorkflowRuntimeState{
		InstanceId: "wf",
		NewEvents: []*backend.HistoryEvent{{
			EventType: &protos.HistoryEvent_ExecutionStarted{
				ExecutionStarted: &protos.ExecutionStartedEvent{
					Name:             "parent",
					WorkflowInstance: &protos.WorkflowInstance{InstanceId: "wf", ExecutionId: minted},
				},
			},
		}},
		PendingMessages: []*backend.WorkflowRuntimeStateMessage{
			child("wf-child-a"),
			child("wf-child-b"),
			{
				// A completion owed to this instance's own parent: not a
				// child creation, must be left alone.
				TargetInstanceId: "grandparent",
				HistoryEvent: &backend.HistoryEvent{
					EventType: &protos.HistoryEvent_ChildWorkflowInstanceCompleted{
						ChildWorkflowInstanceCompleted: &protos.ChildWorkflowInstanceCompletedEvent{TaskScheduledId: 3},
					},
				},
			},
		},
	}
}

func generationID(rs *backend.WorkflowRuntimeState) string {
	return rs.GetNewEvents()[0].GetExecutionStarted().GetWorkflowInstance().GetExecutionId().GetValue()
}

func Test_pinGenerationExecutionID(t *testing.T) {
	t.Parallel()
	o := &orchestrator{actorID: "wf"}

	t.Run("a re-run of the same turn mints the same id", func(t *testing.T) {
		t.Parallel()
		first, second := canTurnState(), canTurnState()
		require.NotEqual(t, generationID(first), generationID(second), "the engine mints a fresh id per execution")

		o.pinGenerationExecutionID(generationState("gen-1-id", 2), first)
		o.pinGenerationExecutionID(generationState("gen-1-id", 2), second)

		assert.Equal(t, generationID(first), generationID(second))
		_, err := uuid.Parse(generationID(first))
		require.NoError(t, err, "still a UUID")
		assert.NotEqual(t, "gen-1-id", generationID(first))
	})

	t.Run("the new start event and every child creation carry the same id", func(t *testing.T) {
		t.Parallel()
		rs := canTurnState()
		children := rs.GetPendingMessages()[:2]
		ownIDs := make([]string, len(children))
		for i, msg := range children {
			ownIDs[i] = msg.GetHistoryEvent().GetExecutionStarted().GetWorkflowInstance().GetExecutionId().GetValue()
		}

		o.pinGenerationExecutionID(generationState("gen-1-id", 2), rs)

		id := generationID(rs)
		for i, msg := range children {
			es := msg.GetHistoryEvent().GetExecutionStarted()
			assert.Equal(t, id, es.GetParentInstance().GetWorkflowInstance().GetExecutionId().GetValue(),
				"the child must record the parent id the parent will persist")
			assert.Equal(t, ownIDs[i], es.GetWorkflowInstance().GetExecutionId().GetValue(),
				"the child's own execution id is not the parent's concern")
		}
		assert.Nil(t, rs.GetPendingMessages()[2].GetHistoryEvent().GetExecutionStarted(), "the completion message is untouched")
	})

	t.Run("generations and lineages stay distinct", func(t *testing.T) {
		t.Parallel()
		gen2, gen3, other := canTurnState(), canTurnState(), canTurnState()
		o.pinGenerationExecutionID(generationState("gen-1-id", 2), gen2)
		o.pinGenerationExecutionID(generationState("gen-1-id", 3), gen3)
		o.pinGenerationExecutionID(generationState("recreated-gen-1-id", 2), other)

		assert.NotEqual(t, generationID(gen2), generationID(gen3), "the straggler guard needs each generation to differ")
		assert.NotEqual(t, generationID(gen2), generationID(other), "a recreated instance starts a new chain")
	})

	t.Run("a history without an execution id seeds from the actor id", func(t *testing.T) {
		t.Parallel()
		first, second, elsewhere := canTurnState(), canTurnState(), canTurnState()
		o.pinGenerationExecutionID(generationState("", 2), first)
		o.pinGenerationExecutionID(generationState("", 2), second)
		(&orchestrator{actorID: "other-wf"}).pinGenerationExecutionID(generationState("", 2), elsewhere)

		assert.NotEmpty(t, generationID(first))
		assert.Equal(t, generationID(first), generationID(second))
		assert.NotEqual(t, generationID(first), generationID(elsewhere))
	})
}
