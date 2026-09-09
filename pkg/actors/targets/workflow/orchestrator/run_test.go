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
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api"
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
// abandonedCANRun drives runWorkflow with a scheduler that overwrites the
// work item state with canState (a continued-as-new runtime state) and then
// abandons the work item, returning the orchestrator and every reminder
// Create request issued. inbox is the consumed inbox of the abandoned turn.
func abandonedCANRun(t *testing.T, instanceID string, inbox []*backend.HistoryEvent, canState *protos.WorkflowRuntimeState) (*orchestrator, []*actorapi.CreateReminderRequest) {
	t.Helper()

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

	scheduler := func(_ context.Context, wi *backend.WorkflowWorkItem) error {
		proto.Reset(wi.State)
		proto.Merge(wi.State, canState)
		wi.Properties[todo.CallbackChannelProperty].(chan bool) <- false
		return nil
	}

	var (
		mu         sync.Mutex
		gotCreates []*actorapi.CreateReminderRequest
	)
	remFake := remindersfake.New().WithCreate(func(_ context.Context, req *actorapi.CreateReminderRequest) error {
		mu.Lock()
		defer mu.Unlock()
		gotCreates = append(gotCreates, req)
		return nil
	})

	fact, err := New(t.Context(), Options{
		AppID:             "testapp",
		WorkflowActorType: "workflow",
		ActivityActorType: "activity",
		Scheduler:         scheduler,
		ActorTypeBuilder:  common.NewActorTypeBuilder("default"),
		Actors: fake.New().WithReminders(func(context.Context) (actorreminders.Interface, error) {
			return remFake, nil
		}),
	})
	require.NoError(t, err)

	o := fact.GetOrCreate(instanceID).(*orchestrator)
	o.state = state
	o.rstate = rstate
	o.ometa = o.ometaFromState(rstate, startEvent.GetExecutionStarted())

	reminder := &actorapi.Reminder{Name: "new-event-test"}
	generation := state.Generation
	completed, runErr := o.runWorkflow(t.Context(), reminder)
	assert.Equal(t, generation+1, o.state.Generation)
	assert.Equal(t, todo.RunCompletedFalse, completed)
	require.Error(t, runErr)

	mu.Lock()
	defer mu.Unlock()
	return o, gotCreates
}

func canRuntimeState(instanceID, input string, extra ...*protos.HistoryEvent) *protos.WorkflowRuntimeState {
	return &protos.WorkflowRuntimeState{
		InstanceId:     instanceID,
		ContinuedAsNew: true,
		StartEvent: &protos.ExecutionStartedEvent{
			Name:  "TestWorkflow",
			Input: wrapperspb.String(input),
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
						Input: wrapperspb.String(input),
						WorkflowInstance: &protos.WorkflowInstance{
							InstanceId: instanceID,
						},
					},
				},
			},
		}, extra...),
	}
}

// Test_runWorkflow_canSaveMovesCarryoverToInbox verifies that when the
// engine abandons a work item after continuing-as-new, the newest generation
// is persisted as a pending start: its ExecutionStarted leads the inbox,
// followed by the carryover EventRaised events, history is empty, and a
// start reminder drives the retry. The consumed inbox is discarded.
func Test_runWorkflow_canSaveMovesCarryoverToInbox(t *testing.T) {
	const instanceID = "test-can-carryover"

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
	carryover := inbox[3:]

	o, creates := abandonedCANRun(t, instanceID, inbox, canRuntimeState(instanceID, `3`, carryover...))

	require.Len(t, o.state.Inbox, len(carryover)+1)
	assert.Equal(t, `3`, o.state.Inbox[0].GetExecutionStarted().GetInput().GetValue())
	for i, e := range carryover {
		assert.NotNil(t, o.state.Inbox[i+1].GetEventRaised())
		assert.Equal(t, e.GetEventId(), o.state.Inbox[i+1].GetEventId())
	}
	assert.Empty(t, o.state.History)

	require.Len(t, creates, 1)
	assert.True(t, strings.HasPrefix(creates[0].Name, reminderPrefixStart), creates[0].Name)
	assert.Equal(t, instanceID, creates[0].ActorID)
}

// Test_runWorkflow_canAbandonWithoutCarryoverDiscardsConsumedInbox verifies
// that the consumed inbox of an abandoned continued-as-new turn is not
// re-delivered into the new generation even when there is no carryover: the
// previous generation's resolutions would otherwise land ahead of operations
// of the new generation that reuse their event IDs.
func Test_runWorkflow_canAbandonWithoutCarryoverDiscardsConsumedInbox(t *testing.T) {
	const instanceID = "test-can-no-carryover"

	inbox := []*backend.HistoryEvent{
		{
			EventId:   -1,
			Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_TimerFired{
				TimerFired: &protos.TimerFiredEvent{TimerId: 1},
			},
		},
		{
			EventId:   -1,
			Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_ChildWorkflowInstanceCompleted{
				ChildWorkflowInstanceCompleted: &protos.ChildWorkflowInstanceCompletedEvent{
					TaskScheduledId: 0,
				},
			},
		},
	}

	o, creates := abandonedCANRun(t, instanceID, inbox, canRuntimeState(instanceID, `1`))

	require.Len(t, o.state.Inbox, 1)
	assert.Equal(t, `1`, o.state.Inbox[0].GetExecutionStarted().GetInput().GetValue())
	assert.Empty(t, o.state.History)
	assert.Empty(t, o.rstate.GetOldEvents())

	names := make([]string, 0, len(creates))
	for _, c := range creates {
		names = append(names, c.Name)
	}
	require.Len(t, creates, 1, "%v", names)
	assert.True(t, strings.HasPrefix(creates[0].Name, reminderPrefixStart), creates[0].Name)
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
			actorTypeBuilder:   common.NewActorTypeBuilder("default"),
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
		{"canceled falls back to failed", api.RUNTIME_STATUS_CANCELED, diag.StatusFailed},
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
		assert.True(t, strings.HasPrefix(ops[1], "create:"+reminderPrefixStart),
			"the pending start reminder must be created after the save, got %q", ops[1])
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
		assert.Len(t, o.state.Inbox, 2, "the pending start and the carryover must be durable in the inbox")
		assert.NotNil(t, o.state.Inbox[0].GetExecutionStarted())
		assert.NotNil(t, o.state.Inbox[1].GetEventRaised())
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

func Test_staleTurnDuplicate(t *testing.T) {
	t.Parallel()

	task := func(id int32) *backend.HistoryEvent {
		return &protos.HistoryEvent{EventId: id, EventType: &protos.HistoryEvent_TaskScheduled{TaskScheduled: &protos.TaskScheduledEvent{Name: "act"}}}
	}
	timer := func(id int32) *backend.HistoryEvent {
		return &protos.HistoryEvent{EventId: id, EventType: &protos.HistoryEvent_TimerCreated{TimerCreated: &protos.TimerCreatedEvent{}}}
	}
	child := func(id int32) *backend.HistoryEvent {
		return &protos.HistoryEvent{EventId: id, EventType: &protos.HistoryEvent_ChildWorkflowInstanceCreated{ChildWorkflowInstanceCreated: &protos.ChildWorkflowInstanceCreatedEvent{Name: "child"}}}
	}
	completed := func(id int32) *backend.HistoryEvent {
		return &protos.HistoryEvent{EventId: -1, EventType: &protos.HistoryEvent_TaskCompleted{TaskCompleted: &protos.TaskCompletedEvent{TaskScheduledId: id}}}
	}
	started := &protos.HistoryEvent{EventId: -1, EventType: &protos.HistoryEvent_ExecutionStarted{ExecutionStarted: &protos.ExecutionStartedEvent{Name: "wf"}}}

	tests := map[string]struct {
		history  []*backend.HistoryEvent
		new      []*backend.HistoryEvent
		wantKind string
		wantID   int32
		stale    bool
	}{
		"no new operations": {
			history: []*backend.HistoryEvent{started, task(0)},
			new:     []*backend.HistoryEvent{completed(0)},
		},
		"new operation with a fresh id": {
			history: []*backend.HistoryEvent{started, task(0), completed(0)},
			new:     []*backend.HistoryEvent{task(1)},
		},
		"same id but different kind": {
			history: []*backend.HistoryEvent{started, task(0), completed(0)},
			new:     []*backend.HistoryEvent{timer(0)},
		},
		"task re-created (the F1 stale turn)": {
			history:  []*backend.HistoryEvent{started, task(0), completed(0)},
			new:      []*backend.HistoryEvent{task(0)},
			wantKind: "task",
			wantID:   0,
			stale:    true,
		},
		"timer re-created": {
			history:  []*backend.HistoryEvent{started, timer(3)},
			new:      []*backend.HistoryEvent{task(4), timer(3)},
			wantKind: "timer",
			wantID:   3,
			stale:    true,
		},
		"child re-created": {
			history:  []*backend.HistoryEvent{started, child(2)},
			new:      []*backend.HistoryEvent{child(2)},
			wantKind: "child",
			wantID:   2,
			stale:    true,
		},
		"empty history": {
			new: []*backend.HistoryEvent{task(0)},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			kind, id, stale := staleTurnDuplicate(&wfenginestate.State{History: test.history}, &backend.WorkflowRuntimeState{NewEvents: test.new})
			assert.Equal(t, test.stale, stale)
			assert.Equal(t, test.wantKind, kind)
			assert.Equal(t, test.wantID, id)
		})
	}
}
