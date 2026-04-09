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
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
)

// Test_runWorkflow_stateIsolation verifies that the cached runtime state
// (o.rstate) is not corrupted when the workflow engine mutates wi.State during
// execution and then fails. This is the scenario that occurs when the
// ContinueAsNew tight-loop exceeds MaxContinueAsNewCount: the applier
// overwrites *wi.State via *s = *newState, and without cloning, o.rstate
// (which was the same pointer) would be left in a corrupted state that is
// inconsistent with the persisted store. On retry, the corrupted rstate would
// cause the workflow to see wrong input, leading to event loss.
func Test_runWorkflow_stateIsolation(t *testing.T) {
	const instanceID = "test-workflow-1"

	startEvent := &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{
				Name:  "TestWorkflow",
				Input: wrapperspb.String(`0`),
				OrchestrationInstance: &protos.OrchestrationInstance{
					InstanceId: instanceID,
				},
			},
		},
	}

	history := []*backend.HistoryEvent{
		{
			EventId: -1, Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_OrchestratorStarted{
				OrchestratorStarted: &protos.OrchestratorStartedEvent{},
			},
		},
		startEvent,
	}

	inbox := make([]*backend.HistoryEvent, 5)
	for i := range inbox {
		inbox[i] = &protos.HistoryEvent{
			EventId:   int32(i),
			Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_EventRaised{
				EventRaised: &protos.EventRaisedEvent{
					Name: "incr",
				},
			},
		}
	}

	state := wfenginestate.NewState(wfenginestate.Options{
		AppID:             "testapp",
		WorkflowActorType: "workflow",
		ActivityActorType: "activity",
	})
	for _, e := range inbox {
		state.AddToInbox(e)
	}
	for _, e := range history {
		state.AddToHistory(e)
	}

	rstate := runtimestate.NewOrchestrationRuntimeState(instanceID, nil, history)

	originalRstate := proto.Clone(rstate).(*backend.OrchestrationRuntimeState)

	schedulerCalled := false
	scheduler := func(_ context.Context, wi *backend.OrchestrationWorkItem) error {
		schedulerCalled = true

		// Simulate a non-CAN failure (e.g. gRPC stream disconnect) where
		// the engine mutates wi.State but does NOT set ContinuedAsNew.
		// Without proto.Clone, this mutation would corrupt o.rstate.
		newState := &protos.OrchestrationRuntimeState{
			InstanceId:     instanceID,
			ContinuedAsNew: false,
			StartEvent: &protos.ExecutionStartedEvent{
				Name:  "TestWorkflow",
				Input: wrapperspb.String(`999`), // Corrupted input
				OrchestrationInstance: &protos.OrchestrationInstance{
					InstanceId: instanceID,
				},
			},
			OldEvents: []*protos.HistoryEvent{},
			NewEvents: []*protos.HistoryEvent{},
		}
		// Overwrite wi.State in place, same as the real applier does
		// (*s = *newState) but without copying the protobuf mutex.
		proto.Reset(wi.State)
		proto.Merge(wi.State, newState)

		wi.Properties[todo.CallbackChannelProperty].(chan bool) <- false
		return nil
	}

	fact, err := New(t.Context(), Options{
		AppID:             "testapp",
		WorkflowActorType: "workflow",
		ActivityActorType: "activity",
		Scheduler:         scheduler,
		ActorTypeBuilder:  common.NewActorTypeBuilder("default"),
		Actors:            fake.New(),
	})
	require.NoError(t, err)

	o := fact.GetOrCreate(instanceID).(*orchestrator)

	o.state = state
	o.rstate = rstate
	o.ometa = o.ometaFromState(rstate, startEvent.GetExecutionStarted())

	reminder := &actorapi.Reminder{Name: "new-event-test"}
	completed, runErr := o.runWorkflow(t.Context(), reminder)

	require.True(t, schedulerCalled, "scheduler should have been called")
	assert.Equal(t, todo.RunCompletedFalse, completed)
	require.Error(t, runErr)

	// CRITICAL ASSERTION: o.rstate must NOT have been mutated by the scheduler's
	// modification of wi.State. Without the proto.Clone fix, o.rstate would
	// point to the same object as wi.State, so the scheduler's *wi.State =
	// *newState would corrupt o.rstate.
	assert.True(t, proto.Equal(originalRstate, o.rstate),
		"o.rstate should not be mutated after failed execution;\n"+
			"got StartEvent.Input=%v, want StartEvent.Input=%v",
		o.rstate.GetStartEvent().GetInput().GetValue(),
		originalRstate.GetStartEvent().GetInput().GetValue(),
	)

	assert.Equal(t,
		originalRstate.GetStartEvent().GetInput().GetValue(),
		o.rstate.GetStartEvent().GetInput().GetValue(),
		"workflow input in cached rstate should be unchanged after failed execution",
	)

	assert.False(t, o.rstate.GetContinuedAsNew(),
		"ContinuedAsNew should not be set on cached rstate after failed execution",
	)
}

// Test_runWorkflow_canSaveMovesCarryoverToInbox verifies that when CAN
// progress is saved (the engine exceeded MaxContinueAsNewCount), carryover
// EventRaised events are moved from History to Inbox. This prevents duplicate
// event delivery on retry: without the move, the retry would pass all
// original inbox events as NewEvents alongside the carryover OldEvents,
// causing the workflow to see and process duplicate events.
func Test_runWorkflow_canSaveMovesCarryoverToInbox(t *testing.T) {
	const instanceID = "test-can-carryover"

	startEvent := &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{
				Name:  "TestWorkflow",
				Input: wrapperspb.String(`0`),
				OrchestrationInstance: &protos.OrchestrationInstance{
					InstanceId: instanceID,
				},
			},
		},
	}

	history := []*backend.HistoryEvent{
		{
			EventId: -1, Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_OrchestratorStarted{
				OrchestratorStarted: &protos.OrchestratorStartedEvent{},
			},
		},
		startEvent,
	}

	inbox := make([]*backend.HistoryEvent, 5)
	for i := range inbox {
		inbox[i] = &protos.HistoryEvent{
			EventId:   int32(i),
			Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_EventRaised{
				EventRaised: &protos.EventRaisedEvent{
					Name: "incr",
				},
			},
		}
	}

	state := wfenginestate.NewState(wfenginestate.Options{
		AppID:             "testapp",
		WorkflowActorType: "workflow",
		ActivityActorType: "activity",
	})
	for _, e := range inbox {
		state.AddToInbox(e)
	}
	for _, e := range history {
		state.AddToHistory(e)
	}

	rstate := runtimestate.NewOrchestrationRuntimeState(instanceID, nil, history)

	carryover := inbox[3:]
	canState := &protos.OrchestrationRuntimeState{
		InstanceId:     instanceID,
		ContinuedAsNew: true,
		StartEvent: &protos.ExecutionStartedEvent{
			Name:  "TestWorkflow",
			Input: wrapperspb.String(`3`),
			OrchestrationInstance: &protos.OrchestrationInstance{
				InstanceId: instanceID,
			},
		},
		OldEvents: []*protos.HistoryEvent{},
		NewEvents: append([]*protos.HistoryEvent{
			{
				EventId: -1, Timestamp: timestamppb.Now(),
				EventType: &protos.HistoryEvent_OrchestratorStarted{
					OrchestratorStarted: &protos.OrchestratorStartedEvent{},
				},
			},
			{
				EventId:   -1,
				Timestamp: timestamppb.Now(),
				EventType: &protos.HistoryEvent_ExecutionStarted{
					ExecutionStarted: &protos.ExecutionStartedEvent{
						Name:  "TestWorkflow",
						Input: wrapperspb.String(`3`),
						OrchestrationInstance: &protos.OrchestrationInstance{
							InstanceId: instanceID,
						},
					},
				},
			},
		}, carryover...),
	}

	scheduler := func(_ context.Context, wi *backend.OrchestrationWorkItem) error {
		proto.Reset(wi.State)
		proto.Merge(wi.State, canState)
		wi.Properties[todo.CallbackChannelProperty].(chan bool) <- false
		return nil
	}

	fact, err := New(t.Context(), Options{
		AppID:             "testapp",
		WorkflowActorType: "workflow",
		ActivityActorType: "activity",
		Scheduler:         scheduler,
		ActorTypeBuilder:  common.NewActorTypeBuilder("default"),
		Actors:            fake.New(),
	})
	require.NoError(t, err)

	o := fact.GetOrCreate(instanceID).(*orchestrator)
	o.state = state
	o.rstate = rstate
	o.ometa = o.ometaFromState(rstate, startEvent.GetExecutionStarted())

	reminder := &actorapi.Reminder{Name: "new-event-test"}
	completed, runErr := o.runWorkflow(t.Context(), reminder)

	assert.Equal(t, todo.RunCompletedFalse, completed)
	require.Error(t, runErr)

	assert.Len(t, o.state.Inbox, len(carryover))

	for _, e := range o.state.History {
		assert.Nil(t, e.GetEventRaised())
	}

	hasExecutionStarted := false
	for _, e := range o.state.History {
		if e.GetExecutionStarted() != nil {
			hasExecutionStarted = true
			break
		}
	}
	assert.True(t, hasExecutionStarted)

	assert.Equal(t, `3`, o.rstate.GetStartEvent().GetInput().GetValue())
}
