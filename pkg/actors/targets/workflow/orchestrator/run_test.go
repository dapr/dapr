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
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/fake"
	actorreminders "github.com/dapr/dapr/pkg/actors/reminders"
	remindersfake "github.com/dapr/dapr/pkg/actors/reminders/fake"
	actorstate "github.com/dapr/dapr/pkg/actors/state"
	statefake "github.com/dapr/dapr/pkg/actors/state/fake"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/signing"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	staterrors "github.com/dapr/dapr/pkg/runtime/wfengine/state/errors"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
)

// Test_runWorkflow_stateIsolation verifies that a runtime state corrupted by
// the workflow engine is never retained when execution fails. The scenario is
// the ContinueAsNew tight-loop exceeding MaxContinueAsNewCount: the applier
// overwrites *wi.State via *s = *newState, and o.rstate is the same pointer.
// The failure path must invalidate the cached state entirely so the retried
// reminder reloads durable truth from the store instead of running on the
// corrupted rstate (which would make the workflow see wrong input and lose
// events).
func Test_runWorkflow_stateIsolation(t *testing.T) {
	const instanceID = "test-workflow-1"

	startEvent := &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{
				Name:  "TestWorkflow",
				Input: wrapperspb.String(`0`),
				WorkflowInstance: &protos.WorkflowInstance{
					InstanceId: instanceID,
				},
			},
		},
	}

	history := []*backend.HistoryEvent{
		{
			EventId: -1, Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_WorkflowStarted{
				WorkflowStarted: &protos.WorkflowStartedEvent{},
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

	rstate := runtimestate.NewWorkflowRuntimeState(instanceID, nil, history)

	originalRstate := proto.Clone(rstate).(*backend.WorkflowRuntimeState)

	schedulerCalled := false
	scheduler := func(_ context.Context, wi *backend.WorkflowWorkItem) error {
		schedulerCalled = true

		// Simulate a non-CAN failure (e.g. gRPC stream disconnect) where
		// the engine mutates wi.State but does NOT set ContinuedAsNew.
		// Without proto.Clone, this mutation would corrupt o.rstate.
		newState := &protos.WorkflowRuntimeState{
			InstanceId:     instanceID,
			ContinuedAsNew: false,
			StartEvent: &protos.ExecutionStartedEvent{
				Name:  "TestWorkflow",
				Input: wrapperspb.String(`999`), // Corrupted input
				WorkflowInstance: &protos.WorkflowInstance{
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

	// CRITICAL ASSERTION: the corrupted rstate must not be retained. The
	// non-CAN abandon path invalidates the whole cached state, so the
	// retried reminder reloads durable truth from the store rather than
	// running on the engine-mutated rstate.
	assert.Nil(t, o.rstate,
		"cached rstate must be invalidated after failed execution, not retained corrupted (StartEvent.Input=%v)",
		o.rstate.GetStartEvent().GetInput().GetValue(),
	)
	assert.Nil(t, o.state, "cached state must be invalidated after failed execution")
	assert.Nil(t, o.ometa, "cached metadata must be invalidated after failed execution")

	// The corruption stayed on the abandoned work item, isolated from the
	// original cached view.
	assert.False(t, proto.Equal(originalRstate, rstate),
		"the engine mutation should have landed on the abandoned work item state")
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
				WorkflowInstance: &protos.WorkflowInstance{
					InstanceId: instanceID,
				},
			},
		},
	}

	history := []*backend.HistoryEvent{
		{
			EventId: -1, Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_WorkflowStarted{
				WorkflowStarted: &protos.WorkflowStartedEvent{},
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

	rstate := runtimestate.NewWorkflowRuntimeState(instanceID, nil, history)

	carryover := inbox[3:]
	canState := &protos.WorkflowRuntimeState{
		InstanceId:     instanceID,
		ContinuedAsNew: true,
		StartEvent: &protos.ExecutionStartedEvent{
			Name:  "TestWorkflow",
			Input: wrapperspb.String(`3`),
			WorkflowInstance: &protos.WorkflowInstance{
				InstanceId: instanceID,
			},
		},
		OldEvents: []*protos.HistoryEvent{},
		NewEvents: append([]*protos.HistoryEvent{
			{
				EventId: -1, Timestamp: timestamppb.Now(),
				EventType: &protos.HistoryEvent_WorkflowStarted{
					WorkflowStarted: &protos.WorkflowStartedEvent{},
				},
			},
			{
				EventId:   -1,
				Timestamp: timestamppb.Now(),
				EventType: &protos.HistoryEvent_ExecutionStarted{
					ExecutionStarted: &protos.ExecutionStartedEvent{
						Name:  "TestWorkflow",
						Input: wrapperspb.String(`3`),
						WorkflowInstance: &protos.WorkflowInstance{
							InstanceId: instanceID,
						},
					},
				},
			},
		}, carryover...),
	}

	scheduler := func(_ context.Context, wi *backend.WorkflowWorkItem) error {
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

// Test_runWorkflow_emptyInboxTerminalCreatesRetentionReminder verifies the
// recovery code path added for orphaned-completed-workflows: when a reminder
// fires on a workflow whose state is already terminal but whose inbox is
// empty (because a previous run drained the inbox and saved completion, but
// the retention reminder Create RPC was lost mid-flight to the scheduler),
// runWorkflow re-issues the retention reminder Create idempotently.
//
// Without this path, a completed workflow whose retention reminder was lost
// would never be purged, even after retention period elapses.
func Test_runWorkflow_emptyInboxTerminalCreatesRetentionReminder(t *testing.T) {
	t.Parallel()

	const instanceID = "wf-empty-inbox-terminal"
	completedAt := time.Now().Add(-1 * time.Hour)

	history := []*backend.HistoryEvent{
		{
			EventId: -1, Timestamp: timestamppb.New(completedAt),
			EventType: &protos.HistoryEvent_WorkflowStarted{
				WorkflowStarted: &protos.WorkflowStartedEvent{},
			},
		},
		{
			EventId: -1, Timestamp: timestamppb.New(completedAt),
			EventType: &protos.HistoryEvent_ExecutionStarted{
				ExecutionStarted: &protos.ExecutionStartedEvent{
					Name: "TestWorkflow",
					WorkflowInstance: &protos.WorkflowInstance{
						InstanceId: instanceID,
					},
				},
			},
		},
		{
			EventId: -1, Timestamp: timestamppb.New(completedAt),
			EventType: &protos.HistoryEvent_ExecutionCompleted{
				ExecutionCompleted: &protos.ExecutionCompletedEvent{
					WorkflowStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED,
				},
			},
		},
	}

	state := wfenginestate.NewState(wfenginestate.Options{
		AppID:             "testapp",
		WorkflowActorType: "dapr.internal.default.testapp.workflow",
		ActivityActorType: "dapr.internal.default.testapp.activity",
	})
	for _, e := range history {
		state.AddToHistory(e)
	}
	// No inbox events: this is the early-exit precondition.

	rstate := runtimestate.NewWorkflowRuntimeState(instanceID, nil, history)
	require.True(t, runtimestate.IsCompleted(rstate),
		"precondition: rstate must be terminal for the early-exit path to fire")

	var (
		mu         sync.Mutex
		gotCreates []*actorapi.CreateReminderRequest
	)
	reminders := remindersfake.New().WithCreate(func(_ context.Context, req *actorapi.CreateReminderRequest) error {
		mu.Lock()
		defer mu.Unlock()
		gotCreates = append(gotCreates, req)
		return nil
	})

	retentionDur := time.Hour
	o := &orchestrator{
		factory: &factory{
			appID:              "testapp",
			actorType:          "dapr.internal.default.testapp.workflow",
			activityActorType:  "dapr.internal.default.testapp.activity",
			retentionActorType: "dapr.internal.default.testapp.retentioner",
			reminders:          reminders,
			// The empty-inbox path always reloads from the store before
			// acting; serve the same terminal state so the recovery path
			// operates on durable truth.
			actorState:       fakeStoreServingState(t, 0, history, nil),
			actorTypeBuilder: common.NewActorTypeBuilder("default"),
			retentionPolicy: &config.WorkflowStateRetentionPolicy{
				AnyTerminal: &retentionDur,
			},
		},
		actorID: instanceID,
		state:   state,
		rstate:  rstate,
	}

	// Simulate a stale "new-event-..." reminder firing on the now-terminal
	// workflow. The first run that completed this workflow already drained
	// the inbox and saved terminal state, but its retention Create may have
	// been lost (this test exercises only the recovery side of that
	// scenario).
	reminder := &actorapi.Reminder{Name: "new-event-stale"}
	completed, err := o.runWorkflow(t.Context(), reminder)
	require.NoError(t, err)
	assert.Equal(t, todo.RunCompletedTrue, completed,
		"runWorkflow should report success so the firing reminder is consumed")

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, gotCreates, 1,
		"expected exactly one Create call for the recovered retention reminder")

	got := gotCreates[0]
	assert.Equal(t, "dapr.internal.default.testapp.retentioner", got.ActorType,
		"retention reminder must target the retentioner actor type")
	assert.Equal(t, instanceID, got.ActorID)
	assert.Equal(t, "retention", got.Name,
		"retention reminder name must be deterministic (no random suffix) so retries overwrite in place")
}

// Test_runWorkflow_emptyInboxTerminalNoRetentionPolicy verifies the recovery
// path is a no-op when no retention policy is configured: the workflow is
// terminal, inbox is empty, but handleRetention returns nil without creating
// any reminder. The firing reminder must still be consumed (RunCompletedTrue).
func Test_runWorkflow_emptyInboxTerminalNoRetentionPolicy(t *testing.T) {
	t.Parallel()

	const instanceID = "wf-no-retention"

	history := []*backend.HistoryEvent{
		{
			EventId: -1, Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_WorkflowStarted{
				WorkflowStarted: &protos.WorkflowStartedEvent{},
			},
		},
		{
			EventId: -1, Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_ExecutionCompleted{
				ExecutionCompleted: &protos.ExecutionCompletedEvent{
					WorkflowStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED,
				},
			},
		},
	}

	state := wfenginestate.NewState(wfenginestate.Options{
		AppID:             "testapp",
		WorkflowActorType: "dapr.internal.default.testapp.workflow",
		ActivityActorType: "dapr.internal.default.testapp.activity",
	})
	for _, e := range history {
		state.AddToHistory(e)
	}

	rstate := runtimestate.NewWorkflowRuntimeState(instanceID, nil, history)

	createCalled := false
	reminders := remindersfake.New().WithCreate(func(_ context.Context, _ *actorapi.CreateReminderRequest) error {
		createCalled = true
		return nil
	})

	o := &orchestrator{
		factory: &factory{
			appID:              "testapp",
			actorType:          "dapr.internal.default.testapp.workflow",
			activityActorType:  "dapr.internal.default.testapp.activity",
			retentionActorType: "dapr.internal.default.testapp.retentioner",
			reminders:          reminders,
			actorState:         fakeStoreServingState(t, 0, history, nil),
			actorTypeBuilder:   common.NewActorTypeBuilder("default"),
			retentionPolicy:    nil,
		},
		actorID: instanceID,
		state:   state,
		rstate:  rstate,
	}

	reminder := &actorapi.Reminder{Name: "new-event-stale"}
	completed, err := o.runWorkflow(t.Context(), reminder)
	require.NoError(t, err)
	assert.Equal(t, todo.RunCompletedTrue, completed)
	assert.False(t, createCalled,
		"no retention reminder should be created when no retention policy is configured")
}

// Test_runWorkflow_emptyInboxNonTerminalSkipsRetention verifies the recovery
// path does not fire on a non-terminal workflow with an empty inbox. The
// existing comment notes this can happen when batch event processing leaves
// stale reminders behind: the runtime must consume the reminder without
// touching the retention reminder.
func Test_runWorkflow_emptyInboxNonTerminalSkipsRetention(t *testing.T) {
	t.Parallel()

	const instanceID = "wf-non-terminal"

	history := []*backend.HistoryEvent{
		{
			EventId: -1, Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_WorkflowStarted{
				WorkflowStarted: &protos.WorkflowStartedEvent{},
			},
		},
		{
			EventId: -1, Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_ExecutionStarted{
				ExecutionStarted: &protos.ExecutionStartedEvent{
					Name: "TestWorkflow",
					WorkflowInstance: &protos.WorkflowInstance{
						InstanceId: instanceID,
					},
				},
			},
		},
	}

	state := wfenginestate.NewState(wfenginestate.Options{
		AppID:             "testapp",
		WorkflowActorType: "dapr.internal.default.testapp.workflow",
		ActivityActorType: "dapr.internal.default.testapp.activity",
	})
	for _, e := range history {
		state.AddToHistory(e)
	}

	rstate := runtimestate.NewWorkflowRuntimeState(instanceID, nil, history)
	require.False(t, runtimestate.IsCompleted(rstate),
		"precondition: rstate must be non-terminal for this case")

	createCalled := false
	reminders := remindersfake.New().WithCreate(func(_ context.Context, _ *actorapi.CreateReminderRequest) error {
		createCalled = true
		return nil
	})

	// The empty-inbox+non-terminal path drops the in-memory cache and reloads
	// from the store to guard against placement-rebalance staleness. Return an
	// empty payload so the reload reports "no state" and the function returns
	// without touching retention. Verifies the retention guard fires even
	// after the cache-invalidating reload.
	metaETag := "meta-v1"
	metaRow, err := proto.Marshal(&backend.BackendWorkflowStateMetadata{Generation: 1})
	require.NoError(t, err)
	actorState := statefake.New().WithGetFn(func(_ context.Context, req *actorapi.GetStateRequest, _ bool) (*actorapi.StateResponse, error) {
		// A live instance always has its metadata row; an empty one reads
		// as a concurrent purge.
		if req.Key == wfenginestate.MetadataKey {
			return &actorapi.StateResponse{Data: metaRow, ETag: &metaETag}, nil
		}
		return &actorapi.StateResponse{}, nil
	})

	retentionDur := time.Hour
	o := &orchestrator{
		factory: &factory{
			appID:              "testapp",
			actorType:          "dapr.internal.default.testapp.workflow",
			activityActorType:  "dapr.internal.default.testapp.activity",
			retentionActorType: "dapr.internal.default.testapp.retentioner",
			reminders:          reminders,
			actorState:         actorState,
			actorTypeBuilder:   common.NewActorTypeBuilder("default"),
			retentionPolicy: &config.WorkflowStateRetentionPolicy{
				AnyTerminal: &retentionDur,
			},
		},
		actorID: instanceID,
		state:   state,
		rstate:  rstate,
	}

	reminder := &actorapi.Reminder{Name: "new-event-stale"}
	completed, err := o.runWorkflow(t.Context(), reminder)
	require.NoError(t, err)
	assert.Equal(t, todo.RunCompletedTrue, completed)
	assert.False(t, createCalled,
		"retention reminder must not be created for a non-terminal workflow")
}

// Test_executionStatusForRuntimeStatus verifies the terminal-status to
// metric-label mapping: completed -> success, terminated -> terminated, and
// every other terminal status -> failed. RUNTIME_STATUS_CANCELED is included
// to document that a hypothetical cancelled orchestration would be recorded as
// failed; the engine never actually produces this status for a top-level
// workflow.
func Test_executionStatusForRuntimeStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status api.OrchestrationStatus
		want   string
	}{
		{"completed", api.RUNTIME_STATUS_COMPLETED, diag.StatusSuccess},
		{"terminated", api.RUNTIME_STATUS_TERMINATED, diag.StatusTerminated},
		{"failed", api.RUNTIME_STATUS_FAILED, diag.StatusFailed},
		{"canceled", api.RUNTIME_STATUS_CANCELED, diag.StatusCanceled},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, executionStatusForRuntimeStatus(tt.status))
		})
	}
}

func TestFilterValidInboxEvents_EmptyInbox(t *testing.T) {
	t.Parallel()
	state := wfenginestate.NewState(wfenginestate.Options{})
	result := filterValidInboxEvents(state)
	assert.Empty(t, result)
}

func TestFilterValidInboxEvents_TaskCompletedValid(t *testing.T) {
	t.Parallel()
	state := wfenginestate.NewState(wfenginestate.Options{})
	state.History = []*backend.HistoryEvent{
		{EventId: 1, EventType: &protos.HistoryEvent_TaskScheduled{TaskScheduled: &protos.TaskScheduledEvent{Name: "activity1"}}},
	}
	state.Inbox = []*backend.HistoryEvent{
		{EventId: -1, EventType: &protos.HistoryEvent_TaskCompleted{TaskCompleted: &protos.TaskCompletedEvent{TaskScheduledId: 1, Result: wrapperspb.String("ok")}}},
	}
	result := filterValidInboxEvents(state)
	assert.Len(t, result, 1)
}

func TestFilterValidInboxEvents_TaskCompletedNoMatch(t *testing.T) {
	t.Parallel()
	state := wfenginestate.NewState(wfenginestate.Options{})
	state.History = []*backend.HistoryEvent{
		{EventId: 1, EventType: &protos.HistoryEvent_TaskScheduled{TaskScheduled: &protos.TaskScheduledEvent{Name: "activity1"}}},
	}
	state.Inbox = []*backend.HistoryEvent{
		{EventId: -1, EventType: &protos.HistoryEvent_TaskCompleted{TaskCompleted: &protos.TaskCompletedEvent{TaskScheduledId: 999, Result: wrapperspb.String("ok")}}},
	}
	result := filterValidInboxEvents(state)
	assert.Empty(t, result)
}

func TestFilterValidInboxEvents_TaskFailedValid(t *testing.T) {
	t.Parallel()
	state := wfenginestate.NewState(wfenginestate.Options{})
	state.History = []*backend.HistoryEvent{
		{EventId: 2, EventType: &protos.HistoryEvent_TaskScheduled{TaskScheduled: &protos.TaskScheduledEvent{Name: "activity2"}}},
	}
	state.Inbox = []*backend.HistoryEvent{
		{EventId: -1, EventType: &protos.HistoryEvent_TaskFailed{TaskFailed: &protos.TaskFailedEvent{TaskScheduledId: 2}}},
	}
	result := filterValidInboxEvents(state)
	assert.Len(t, result, 1)
}

func TestFilterValidInboxEvents_TaskFailedNoMatch(t *testing.T) {
	t.Parallel()
	state := wfenginestate.NewState(wfenginestate.Options{})
	state.History = []*backend.HistoryEvent{
		{EventId: 2, EventType: &protos.HistoryEvent_TaskScheduled{TaskScheduled: &protos.TaskScheduledEvent{Name: "activity2"}}},
	}
	state.Inbox = []*backend.HistoryEvent{
		{EventId: -1, EventType: &protos.HistoryEvent_TaskFailed{TaskFailed: &protos.TaskFailedEvent{TaskScheduledId: 777}}},
	}
	result := filterValidInboxEvents(state)
	assert.Empty(t, result)
}

func TestFilterValidInboxEvents_ChildWorkflowCompletedValid(t *testing.T) {
	t.Parallel()
	state := wfenginestate.NewState(wfenginestate.Options{})
	state.History = []*backend.HistoryEvent{
		{EventId: 5, EventType: &protos.HistoryEvent_ChildWorkflowInstanceCreated{ChildWorkflowInstanceCreated: &protos.ChildWorkflowInstanceCreatedEvent{}}},
	}
	state.Inbox = []*backend.HistoryEvent{
		{EventId: -1, EventType: &protos.HistoryEvent_ChildWorkflowInstanceCompleted{ChildWorkflowInstanceCompleted: &protos.ChildWorkflowInstanceCompletedEvent{TaskScheduledId: 5}}},
	}
	result := filterValidInboxEvents(state)
	assert.Len(t, result, 1)
}

func TestFilterValidInboxEvents_ChildWorkflowCompletedNoMatch(t *testing.T) {
	t.Parallel()
	state := wfenginestate.NewState(wfenginestate.Options{})
	state.History = []*backend.HistoryEvent{
		{EventId: 5, EventType: &protos.HistoryEvent_ChildWorkflowInstanceCreated{ChildWorkflowInstanceCreated: &protos.ChildWorkflowInstanceCreatedEvent{}}},
	}
	state.Inbox = []*backend.HistoryEvent{
		{EventId: -1, EventType: &protos.HistoryEvent_ChildWorkflowInstanceCompleted{ChildWorkflowInstanceCompleted: &protos.ChildWorkflowInstanceCompletedEvent{TaskScheduledId: 99}}},
	}
	result := filterValidInboxEvents(state)
	assert.Empty(t, result)
}

func TestFilterValidInboxEvents_ChildWorkflowFailedNoMatch(t *testing.T) {
	t.Parallel()
	state := wfenginestate.NewState(wfenginestate.Options{})
	state.History = []*backend.HistoryEvent{
		{EventId: 5, EventType: &protos.HistoryEvent_ChildWorkflowInstanceCreated{ChildWorkflowInstanceCreated: &protos.ChildWorkflowInstanceCreatedEvent{}}},
	}
	state.Inbox = []*backend.HistoryEvent{
		{EventId: -1, EventType: &protos.HistoryEvent_ChildWorkflowInstanceFailed{ChildWorkflowInstanceFailed: &protos.ChildWorkflowInstanceFailedEvent{TaskScheduledId: 42}}},
	}
	result := filterValidInboxEvents(state)
	assert.Empty(t, result)
}

func TestFilterValidInboxEvents_EventRaisedPassesThrough(t *testing.T) {
	t.Parallel()
	state := wfenginestate.NewState(wfenginestate.Options{})
	state.Inbox = []*backend.HistoryEvent{
		{EventId: -1, EventType: &protos.HistoryEvent_EventRaised{EventRaised: &protos.EventRaisedEvent{Name: "myevent"}}},
	}
	result := filterValidInboxEvents(state)
	assert.Len(t, result, 1)
}

func TestFilterValidInboxEvents_MixedValidAndInvalid(t *testing.T) {
	t.Parallel()
	state := wfenginestate.NewState(wfenginestate.Options{})
	state.History = []*backend.HistoryEvent{
		{EventId: 1, EventType: &protos.HistoryEvent_TaskScheduled{TaskScheduled: &protos.TaskScheduledEvent{Name: "activity1"}}},
		{EventId: 5, EventType: &protos.HistoryEvent_ChildWorkflowInstanceCreated{ChildWorkflowInstanceCreated: &protos.ChildWorkflowInstanceCreatedEvent{}}},
	}
	state.Inbox = []*backend.HistoryEvent{
		{EventId: -1, EventType: &protos.HistoryEvent_TaskCompleted{TaskCompleted: &protos.TaskCompletedEvent{TaskScheduledId: 1, Result: wrapperspb.String("ok")}}},
		{EventId: -1, EventType: &protos.HistoryEvent_TaskCompleted{TaskCompleted: &protos.TaskCompletedEvent{TaskScheduledId: 999, Result: wrapperspb.String("injected")}}},
		{EventId: -1, EventType: &protos.HistoryEvent_ChildWorkflowInstanceCompleted{ChildWorkflowInstanceCompleted: &protos.ChildWorkflowInstanceCompletedEvent{TaskScheduledId: 5}}},
		{EventId: -1, EventType: &protos.HistoryEvent_EventRaised{EventRaised: &protos.EventRaisedEvent{Name: "myevent"}}},
	}
	result := filterValidInboxEvents(state)
	// task 1 valid, task 999 dropped, child 5 valid, event raised kept
	assert.Len(t, result, 3)
}

// Test_runWorkflow_canCarryoverSavesBeforeReminderCreate pins the
// save-before-create ordering of the ContinueAsNew carryover path: creating
// the wake-up reminder before the save lets it fire remotely against un-saved
// state, ack SUCCESS and be deleted, stranding the carryover once the save
// commits.
func Test_runWorkflow_canCarryoverSavesBeforeReminderCreate(t *testing.T) {
	t.Parallel()

	newCanOrchestrator := func(t *testing.T, ops *[]string, lock *sync.Mutex, createErr error) *orchestrator {
		t.Helper()

		const instanceID = "test-can-order"

		startEvent := &protos.HistoryEvent{
			EventId:   -1,
			Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_ExecutionStarted{
				ExecutionStarted: &protos.ExecutionStartedEvent{
					Name:  "TestWorkflow",
					Input: wrapperspb.String(`0`),
					WorkflowInstance: &protos.WorkflowInstance{
						InstanceId: instanceID,
					},
				},
			},
		}

		history := []*backend.HistoryEvent{
			{
				EventId: -1, Timestamp: timestamppb.Now(),
				EventType: &protos.HistoryEvent_WorkflowStarted{
					WorkflowStarted: &protos.WorkflowStartedEvent{},
				},
			},
			startEvent,
		}

		inbox := make([]*backend.HistoryEvent, 3)
		for i := range inbox {
			inbox[i] = &protos.HistoryEvent{
				EventId:   int32(i),
				Timestamp: timestamppb.Now(),
				EventType: &protos.HistoryEvent_EventRaised{
					EventRaised: &protos.EventRaisedEvent{Name: "incr"},
				},
			}
		}

		wfState := wfenginestate.NewState(wfenginestate.Options{
			AppID:             "testapp",
			WorkflowActorType: "workflow",
			ActivityActorType: "activity",
		})
		for _, e := range inbox {
			wfState.AddToInbox(e)
		}
		for _, e := range history {
			wfState.AddToHistory(e)
		}

		canState := &protos.WorkflowRuntimeState{
			InstanceId:     instanceID,
			ContinuedAsNew: true,
			StartEvent: &protos.ExecutionStartedEvent{
				Name:  "TestWorkflow",
				Input: wrapperspb.String(`2`),
				WorkflowInstance: &protos.WorkflowInstance{
					InstanceId: instanceID,
				},
			},
			OldEvents: []*protos.HistoryEvent{},
			NewEvents: append([]*protos.HistoryEvent{
				{
					EventId: -1, Timestamp: timestamppb.Now(),
					EventType: &protos.HistoryEvent_WorkflowStarted{
						WorkflowStarted: &protos.WorkflowStartedEvent{},
					},
				},
			}, inbox[2:]...),
		}

		scheduler := func(_ context.Context, wi *backend.WorkflowWorkItem) error {
			proto.Reset(wi.State)
			proto.Merge(wi.State, canState)
			wi.Properties[todo.CallbackChannelProperty].(chan bool) <- false
			return nil
		}

		fakeRems := remindersfake.New().
			WithCreate(func(_ context.Context, req *actorapi.CreateReminderRequest) error {
				lock.Lock()
				defer lock.Unlock()
				if createErr != nil {
					return createErr
				}
				*ops = append(*ops, "create:"+req.Name)
				return nil
			})

		fakeState := statefake.New().
			WithTransactionalStateOperationFn(func(context.Context, bool, *actorapi.TransactionalRequest, bool) error {
				lock.Lock()
				defer lock.Unlock()
				*ops = append(*ops, "save")
				return nil
			})

		fact, err := New(t.Context(), Options{
			AppID:             "testapp",
			WorkflowActorType: "workflow",
			ActivityActorType: "activity",
			Scheduler:         scheduler,
			ActorTypeBuilder:  common.NewActorTypeBuilder("default"),
			Actors: fake.New().
				WithReminders(func(context.Context) (actorreminders.Interface, error) {
					return fakeRems, nil
				}).
				WithState(func(context.Context) (actorstate.Interface, error) {
					return fakeState, nil
				}),
		})
		require.NoError(t, err)

		o := fact.GetOrCreate(instanceID).(*orchestrator)
		o.state = wfState
		o.rstate = runtimestate.NewWorkflowRuntimeState(instanceID, nil, history)
		o.ometa = o.ometaFromState(o.rstate, startEvent.GetExecutionStarted())

		return o
	}

	t.Run("save happens before the carryover reminder create", func(t *testing.T) {
		t.Parallel()

		var (
			lock sync.Mutex
			ops  []string
		)
		o := newCanOrchestrator(t, &ops, &lock, nil)

		completed, runErr := o.runWorkflow(t.Context(), &actorapi.Reminder{Name: "new-event-test"})
		assert.Equal(t, todo.RunCompletedFalse, completed)
		require.Error(t, runErr)

		lock.Lock()
		defer lock.Unlock()
		require.Len(t, ops, 2)
		assert.Equal(t, "save", ops[0])
		assert.True(t, strings.HasPrefix(ops[1], "create:new-event-"),
			"the carryover wake-up reminder must be created after the save, got %q", ops[1])
	})

	t.Run("reminder create failure is recoverable and keeps the cache", func(t *testing.T) {
		t.Parallel()

		var (
			lock sync.Mutex
			ops  []string
		)
		o := newCanOrchestrator(t, &ops, &lock, errors.New("scheduler exploded"))

		completed, runErr := o.runWorkflow(t.Context(), &actorapi.Reminder{Name: "new-event-test"})
		assert.Equal(t, todo.RunCompletedFalse, completed)
		require.Error(t, runErr)
		assert.True(t, wferrors.IsRecoverable(runErr),
			"a create failure after the save must be recoverable so the driving reminder refires")

		lock.Lock()
		defer lock.Unlock()
		assert.Equal(t, []string{"save"}, ops, "the save must have happened before the failed create")

		require.NotNil(t, o.state, "the cache must not be invalidated: it is consistent with the store post-save")
		assert.Len(t, o.state.Inbox, 1, "the carryover must be durable in the inbox")
	})
}

// fakeStoreServingState returns an actor-state fake whose Get/GetBulk serve
// the given history and inbox as the durable workflow state, in the same key
// layout LoadWorkflowState reads.
func fakeStoreServingState(t *testing.T, generation uint64, history, inbox []*backend.HistoryEvent) *statefake.Fake {
	t.Helper()

	meta := &backend.BackendWorkflowStateMetadata{
		Generation:    generation,
		InboxLength:   uint64(len(inbox)),
		HistoryLength: uint64(len(history)),
	}
	metaData, err := proto.Marshal(meta)
	require.NoError(t, err)

	rows := make(map[string][]byte, len(history)+len(inbox))
	for i, e := range inbox {
		data, merr := proto.Marshal(e)
		require.NoError(t, merr)
		rows[fmt.Sprintf("inbox-%06d", i)] = data
	}
	for i, e := range history {
		data, merr := proto.Marshal(e)
		require.NoError(t, merr)
		rows[fmt.Sprintf("history-%06d", i)] = data
	}

	return statefake.New().
		WithGetFn(func(_ context.Context, req *actorapi.GetStateRequest, _ bool) (*actorapi.StateResponse, error) {
			if req.Key == wfenginestate.MetadataKey {
				return &actorapi.StateResponse{Data: metaData}, nil
			}
			return &actorapi.StateResponse{}, nil
		}).
		WithGetBulkFn(func(_ context.Context, req *actorapi.GetBulkStateRequest, _ bool) (actorapi.BulkStateResponse, error) {
			res := make(actorapi.BulkStateResponse, len(req.Keys))
			for _, k := range req.Keys {
				res[k] = actorapi.BulkStateEntry{Data: rows[k]}
			}
			return res, nil
		})
}

// terminalHistory returns a minimal completed-workflow history for tests.
func terminalHistory(instanceID string) []*backend.HistoryEvent {
	return []*backend.HistoryEvent{
		{
			EventId: -1, Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_WorkflowStarted{
				WorkflowStarted: &protos.WorkflowStartedEvent{},
			},
		},
		{
			EventId: -1, Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_ExecutionStarted{
				ExecutionStarted: &protos.ExecutionStartedEvent{
					Name: "TestWorkflow",
					WorkflowInstance: &protos.WorkflowInstance{
						InstanceId: instanceID,
					},
				},
			},
		},
		{
			EventId: -1, Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_ExecutionCompleted{
				ExecutionCompleted: &protos.ExecutionCompletedEvent{
					WorkflowStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED,
				},
			},
		},
	}
}

// Test_runWorkflow_emptyInboxStaleTerminalCacheReloads pins the instance-ID
// reuse hazard on the empty-inbox path: the cached rstate is terminal from
// the PREVIOUS generation while the store already holds the NEW generation's
// pending start (metadata + ExecutionStarted in the inbox, empty history). A
// terminal cache must not be treated as evidence the stored state is
// terminal: acking RunCompletedTrue off it would delete the reminder the new
// generation needs and re-assert retention against the wrong generation. The
// path must reload from the store and drive the pending start instead.
func Test_runWorkflow_emptyInboxStaleTerminalCacheReloads(t *testing.T) {
	t.Parallel()

	const instanceID = "wf-reused-id"

	prevHistory := terminalHistory(instanceID)

	cached := wfenginestate.NewState(wfenginestate.Options{
		AppID:             "testapp",
		WorkflowActorType: "workflow",
		ActivityActorType: "activity",
	})
	for _, e := range prevHistory {
		cached.AddToHistory(e)
	}

	cachedRstate := runtimestate.NewWorkflowRuntimeState(instanceID, nil, prevHistory)
	require.True(t, runtimestate.IsCompleted(cachedRstate),
		"precondition: the cached rstate must be terminal")

	// The store holds the NEW generation: empty history, pending start in the
	// inbox.
	newGenInbox := []*backend.HistoryEvent{{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{
				Name:  "TestWorkflow",
				Input: wrapperspb.String(`"gen2"`),
				WorkflowInstance: &protos.WorkflowInstance{
					InstanceId: instanceID,
				},
			},
		},
	}}

	var (
		mu         sync.Mutex
		gotCreates []string
	)
	fakeRems := remindersfake.New().WithCreate(func(_ context.Context, req *actorapi.CreateReminderRequest) error {
		mu.Lock()
		defer mu.Unlock()
		gotCreates = append(gotCreates, req.Name)
		return nil
	})

	schedulerCalled := false
	scheduler := func(_ context.Context, wi *backend.WorkflowWorkItem) error {
		schedulerCalled = true
		require.Len(t, wi.NewEvents, 1)
		assert.NotNil(t, wi.NewEvents[0].GetExecutionStarted(),
			"the turn must run against the reloaded pending start")
		// Abandon the work item; driving the app is not under test here.
		wi.Properties[todo.CallbackChannelProperty].(chan bool) <- false
		return nil
	}

	retentionDur := time.Hour
	fact, err := New(t.Context(), Options{
		AppID:              "testapp",
		WorkflowActorType:  "workflow",
		ActivityActorType:  "activity",
		RetentionActorType: "retentioner",
		Scheduler:          scheduler,
		ActorTypeBuilder:   common.NewActorTypeBuilder("default"),
		RetentionPolicy: &config.WorkflowStateRetentionPolicy{
			AnyTerminal: &retentionDur,
		},
		Actors: fake.New().
			WithReminders(func(context.Context) (actorreminders.Interface, error) {
				return fakeRems, nil
			}).
			WithState(func(context.Context) (actorstate.Interface, error) {
				return fakeStoreServingState(t, 1, nil, newGenInbox), nil
			}),
	})
	require.NoError(t, err)

	o := fact.GetOrCreate(instanceID).(*orchestrator)
	o.state = cached
	o.rstate = cachedRstate
	o.ometa = o.ometaFromState(cachedRstate, nil)

	completed, runErr := o.runWorkflow(t.Context(), &actorapi.Reminder{Name: "new-event-recreated"})

	assert.True(t, schedulerCalled,
		"the reloaded pending start must be driven, not acked away off the stale terminal cache")
	assert.Equal(t, todo.RunCompletedFalse, completed,
		"the reminder must not be consumed: the new generation still needs it")
	require.Error(t, runErr)

	mu.Lock()
	defer mu.Unlock()
	assert.NotContains(t, gotCreates, "retention",
		"retention must not be re-asserted off the stale terminal cache")
}

// Test_runWorkflow_unstartableStateFailsTerminally verifies that a work item
// against a durable state that can never make progress (inbox events, empty
// history, no pending ExecutionStarted: the committed start was lost) fails
// the instance terminally instead of silently dropping the work: an
// ExecutionCompleted(FAILED) with failure details is committed, the inbox is
// cleared, the retention reminder is created, and the driving reminder is
// consumed so redelivery stops.
// With history signing enabled, the unstartable shape must reach the
// terminal-FAILED classification, not the inbox-tamper tombstone: with no
// signed history there is no attestation to violate, and the tombstone
// appends a completion without a start event, leaving the status PENDING.
func Test_runWorkflow_unstartableStateFailsTerminallyWithSigning(t *testing.T) {
	t.Parallel()

	const instanceID = "wf-unstartable-signed"

	inbox := []*backend.HistoryEvent{{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{TaskScheduledId: 2},
		},
	}}

	h := newWakeHarness(t, instanceID, true)
	h.fact.fastPath = true
	h.fact.signer = testAddSigner(t)
	h.orch.signing = &signing.Signing{
		Signer:            h.fact.signer,
		Namespace:         "default",
		ActorID:           instanceID,
		ActorType:         h.fact.actorType,
		ActivityActorType: h.fact.activityActorType,
		Reminders:         h.fact.reminders,
	}
	h.orch.actorState = fakeStoreServingState(t, 0, nil, inbox)
	h.orch.state = nil

	completed, err := h.orch.runWorkflow(t.Context(), &actorapi.Reminder{Name: "new-event-x"})
	require.NoError(t, err)
	assert.Equal(t, todo.RunCompletedTrue, completed)

	require.NotNil(t, h.orch.rstate)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED, runtimestate.RuntimeStatus(h.orch.rstate))
	fd, ferr := runtimestate.FailureDetails(h.orch.rstate)
	require.NoError(t, ferr, "failure details must surface")
	assert.Equal(t, staterrors.ErrorTypeUnstartableState, fd.GetErrorType(),
		"the shape must classify as unstartable, not tampered")
	assert.Empty(t, h.orch.state.Inbox)
}

func Test_runWorkflow_unstartableStateFailsTerminally(t *testing.T) {
	t.Parallel()

	const instanceID = "wf-unstartable"

	inbox := []*backend.HistoryEvent{{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{
				TaskScheduledId: 1,
				Result:          wrapperspb.String(`"orphaned"`),
			},
		},
	}}

	var (
		mu         sync.Mutex
		gotCreates []string
	)
	fakeRems := remindersfake.New().WithCreate(func(_ context.Context, req *actorapi.CreateReminderRequest) error {
		mu.Lock()
		defer mu.Unlock()
		gotCreates = append(gotCreates, req.Name)
		return nil
	})

	var saves int
	store := fakeStoreServingState(t, 1, nil, inbox).
		WithTransactionalStateOperationFn(func(context.Context, bool, *actorapi.TransactionalRequest, bool) error {
			saves++
			return nil
		})

	schedulerCalled := false
	scheduler := func(_ context.Context, wi *backend.WorkflowWorkItem) error {
		schedulerCalled = true
		wi.Properties[todo.CallbackChannelProperty].(chan bool) <- false
		return nil
	}

	retentionDur := time.Hour
	fact, err := New(t.Context(), Options{
		AppID:              "testapp",
		WorkflowActorType:  "workflow",
		ActivityActorType:  "activity",
		RetentionActorType: "retentioner",
		Scheduler:          scheduler,
		ActorTypeBuilder:   common.NewActorTypeBuilder("default"),
		RetentionPolicy: &config.WorkflowStateRetentionPolicy{
			AnyTerminal: &retentionDur,
		},
		Actors: fake.New().
			WithReminders(func(context.Context) (actorreminders.Interface, error) {
				return fakeRems, nil
			}).
			WithState(func(context.Context) (actorstate.Interface, error) {
				return store, nil
			}),
	})
	require.NoError(t, err)

	o := fact.GetOrCreate(instanceID).(*orchestrator)

	completed, runErr := o.runWorkflow(t.Context(), &actorapi.Reminder{Name: "new-event-orphan"})
	require.NoError(t, runErr)
	assert.Equal(t, todo.RunCompletedTrue, completed,
		"the driving reminder must be consumed so redelivery stops")
	assert.False(t, schedulerCalled, "an unstartable state must never reach the engine")
	assert.Equal(t, 1, saves, "the terminal failure must be committed")

	require.NotNil(t, o.state)
	require.Len(t, o.state.History, 2)
	assert.NotNil(t, o.state.History[0].GetExecutionStarted(),
		"a synthetic start must be committed so the FAILED status surfaces (RuntimeStatus reports PENDING without a start event)")
	ec := o.state.History[1].GetExecutionCompleted()
	require.NotNil(t, ec)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED, ec.GetWorkflowStatus())
	require.NotNil(t, ec.GetFailureDetails())
	assert.Equal(t, staterrors.ErrorTypeUnstartableState, ec.GetFailureDetails().GetErrorType())
	assert.Contains(t, ec.GetFailureDetails().GetErrorMessage(), "no pending ExecutionStarted")
	assert.Empty(t, o.state.Inbox, "the undeliverable inbox must be drained with the failure commit")
	assert.Equal(t, api.RUNTIME_STATUS_FAILED, runtimestate.RuntimeStatus(o.rstate))

	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, gotCreates, "retention",
		"a terminally failed instance must get its retention reminder")
}

// Test_runWorkflow_pendingStartEmptyHistoryRuns pins the healthy-pending
// shape the unstartable check must never touch: empty history with an
// ExecutionStarted sitting in the inbox awaiting its start reminder. The turn
// must reach the engine exactly as today.
func Test_runWorkflow_pendingStartEmptyHistoryRuns(t *testing.T) {
	t.Parallel()

	const instanceID = "wf-healthy-pending"

	inbox := []*backend.HistoryEvent{{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{
				Name: "TestWorkflow",
				WorkflowInstance: &protos.WorkflowInstance{
					InstanceId: instanceID,
				},
			},
		},
	}}

	var saves int
	store := fakeStoreServingState(t, 1, nil, inbox).
		WithTransactionalStateOperationFn(func(context.Context, bool, *actorapi.TransactionalRequest, bool) error {
			saves++
			return nil
		})

	schedulerCalled := false
	scheduler := func(_ context.Context, wi *backend.WorkflowWorkItem) error {
		schedulerCalled = true
		require.Len(t, wi.NewEvents, 1)
		assert.NotNil(t, wi.NewEvents[0].GetExecutionStarted())
		wi.Properties[todo.CallbackChannelProperty].(chan bool) <- false
		return nil
	}

	fact, err := New(t.Context(), Options{
		AppID:             "testapp",
		WorkflowActorType: "workflow",
		ActivityActorType: "activity",
		Scheduler:         scheduler,
		ActorTypeBuilder:  common.NewActorTypeBuilder("default"),
		Actors: fake.New().
			WithState(func(context.Context) (actorstate.Interface, error) {
				return store, nil
			}),
	})
	require.NoError(t, err)

	o := fact.GetOrCreate(instanceID).(*orchestrator)

	completed, runErr := o.runWorkflow(t.Context(), &actorapi.Reminder{Name: "start"})
	assert.True(t, schedulerCalled, "a healthy pending start must be driven")
	assert.Equal(t, todo.RunCompletedFalse, completed)
	require.Error(t, runErr)
	assert.Zero(t, saves, "nothing may be committed for the abandoned healthy turn")
}

// Test_runWorkflow_unstartableCacheButDurableStartableRetries verifies the
// reclassify-before-acting step: when only the CACHE shows the unstartable
// shape but the durable state holds a pending start, the instance must not be
// failed; the turn is retried against the reloaded state.
func Test_runWorkflow_unstartableCacheButDurableStartableRetries(t *testing.T) {
	t.Parallel()

	const instanceID = "wf-stale-unstartable-cache"

	cached := wfenginestate.NewState(wfenginestate.Options{
		AppID:             "testapp",
		WorkflowActorType: "workflow",
		ActivityActorType: "activity",
	})
	cached.AddToInbox(&backend.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{TaskScheduledId: 1},
		},
	})

	durableInbox := []*backend.HistoryEvent{{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{
				Name: "TestWorkflow",
				WorkflowInstance: &protos.WorkflowInstance{
					InstanceId: instanceID,
				},
			},
		},
	}}

	var saves int
	store := fakeStoreServingState(t, 1, nil, durableInbox).
		WithTransactionalStateOperationFn(func(context.Context, bool, *actorapi.TransactionalRequest, bool) error {
			saves++
			return nil
		})

	schedulerCalled := false
	scheduler := func(_ context.Context, wi *backend.WorkflowWorkItem) error {
		schedulerCalled = true
		wi.Properties[todo.CallbackChannelProperty].(chan bool) <- false
		return nil
	}

	fact, err := New(t.Context(), Options{
		AppID:             "testapp",
		WorkflowActorType: "workflow",
		ActivityActorType: "activity",
		Scheduler:         scheduler,
		ActorTypeBuilder:  common.NewActorTypeBuilder("default"),
		Actors: fake.New().
			WithState(func(context.Context) (actorstate.Interface, error) {
				return store, nil
			}),
	})
	require.NoError(t, err)

	o := fact.GetOrCreate(instanceID).(*orchestrator)
	o.state = cached
	o.rstate = runtimestate.NewWorkflowRuntimeState(instanceID, nil, nil)
	o.ometa = o.ometaFromState(o.rstate, nil)

	completed, runErr := o.runWorkflow(t.Context(), &actorapi.Reminder{Name: "new-event-stale"})
	assert.Equal(t, todo.RunCompletedFalse, completed)
	require.Error(t, runErr)
	assert.True(t, wferrors.IsRecoverable(runErr), "the retry must be recoverable so the reminder refires")
	assert.False(t, schedulerCalled, "the stale-cache turn must be retried, not run")
	assert.Zero(t, saves, "the instance must not be failed off a stale cache")

	require.NotNil(t, o.state, "the cache must hold the reloaded durable state")
	require.Len(t, o.state.Inbox, 1)
	assert.NotNil(t, o.state.Inbox[0].GetExecutionStarted())
}
