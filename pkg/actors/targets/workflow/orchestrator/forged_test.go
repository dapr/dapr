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
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/dapr/pkg/actors/reminders"
	remindersfake "github.com/dapr/dapr/pkg/actors/reminders/fake"
	actorstate "github.com/dapr/dapr/pkg/actors/state"
	statefake "github.com/dapr/dapr/pkg/actors/state/fake"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
)

// newForgedOrchestrator returns a running workflow orchestrator for app
// "testapp" whose history has scheduled task 7 locally, and a counter of its
// state saves.
func newForgedOrchestrator(t *testing.T, instanceID string) (*orchestrator, *atomic.Int32) {
	t.Helper()

	startEvent := &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{
				Name:                  "TestWorkflow",
				Input:                 wrapperspb.String(`null`),
				OrchestrationInstance: &protos.OrchestrationInstance{InstanceId: instanceID},
			},
		},
	}
	history := []*backend.HistoryEvent{startEvent, taskScheduledEvent(7, nil)}

	wfState := state.NewState(state.Options{
		AppID:             "testapp",
		WorkflowActorType: "dapr.internal.default.testapp.workflow",
		ActivityActorType: "dapr.internal.default.testapp.activity",
	})
	for _, e := range history {
		wfState.AddToHistory(e)
	}

	var saves atomic.Int32
	store := statefake.New().WithTransactionalStateOperationFn(func(context.Context, bool, *actorapi.TransactionalRequest, bool) error {
		saves.Add(1)
		return nil
	})
	actors := fake.New().
		WithReminders(func(context.Context) (reminders.Interface, error) {
			return remindersfake.New(), nil
		}).
		WithState(func(context.Context) (actorstate.Interface, error) {
			return store, nil
		})

	fact, err := New(t.Context(), Options{
		AppID:             "testapp",
		WorkflowActorType: "dapr.internal.default.testapp.workflow",
		ActivityActorType: "dapr.internal.default.testapp.activity",
		ActorTypeBuilder:  common.NewActorTypeBuilder("default"),
		Actors:            actors,
	})
	require.NoError(t, err)

	o := fact.GetOrCreate(instanceID).(*orchestrator)
	o.state = wfState
	o.rstate = runtimestate.NewOrchestrationRuntimeState(instanceID, nil, history)
	o.ometa = o.ometaFromState(o.rstate, startEvent.GetExecutionStarted())
	return o, &saves
}

func taskScheduledEvent(id int32, targetAppID *string) *backend.HistoryEvent {
	return &protos.HistoryEvent{
		EventId:   id,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskScheduled{TaskScheduled: &protos.TaskScheduledEvent{Name: "act"}},
		Router:    &protos.TaskRouter{SourceAppID: "testapp", TargetAppID: targetAppID},
	}
}

func taskCompletedEvent(id int32) *backend.HistoryEvent {
	return &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{TaskScheduledId: id, Result: wrapperspb.String(`"forged"`)},
		},
	}
}

// An activity-result reminder may carry only an activity result: any other
// event under that name is acked and dropped, from any sender, before it can
// reach the inbox (a TimerFired there panics the save).
func Test_handleReminder_activityResultDropsNonResultPayloads(t *testing.T) {
	t.Parallel()

	payloads := map[string]*protos.HistoryEvent{
		"EventRaised": {EventId: -1, Timestamp: timestamppb.Now(), EventType: &protos.HistoryEvent_EventRaised{
			EventRaised: &protos.EventRaisedEvent{Name: "evt"},
		}},
		"ExecutionTerminated": {EventId: -1, Timestamp: timestamppb.Now(), EventType: &protos.HistoryEvent_ExecutionTerminated{
			ExecutionTerminated: &protos.ExecutionTerminatedEvent{},
		}},
		"TimerFired": {EventId: -1, Timestamp: timestamppb.Now(), EventType: &protos.HistoryEvent_TimerFired{
			TimerFired: &protos.TimerFiredEvent{TimerId: 7},
		}},
		"SubOrchestrationInstanceFailed": {EventId: -1, Timestamp: timestamppb.Now(), EventType: &protos.HistoryEvent_SubOrchestrationInstanceFailed{
			SubOrchestrationInstanceFailed: &protos.SubOrchestrationInstanceFailedEvent{TaskScheduledId: 7},
		}},
	}

	for name, ev := range payloads {
		for _, sender := range []string{"other", "testapp", ""} {
			t.Run(name+" from '"+sender+"'", func(t *testing.T) {
				t.Parallel()
				o, saves := newForgedOrchestrator(t, "test-forged-"+name)

				data, err := anypb.New(ev)
				require.NoError(t, err)
				require.NoError(t, o.handleReminder(t.Context(), &actorapi.Reminder{
					Name:        "activity-result-forged",
					ActorType:   o.actorType,
					ActorID:     o.actorID,
					Data:        data,
					SourceAppID: sender,
				}), "the reminder is acked so the one-shot job is deleted")
				assert.Empty(t, o.state.Inbox)
				assert.Zero(t, saves.Load())
			})
		}
	}
}

// A result created by another app is admitted only for a task this history
// dispatched to that app. A result from another app for a task not yet
// scheduled is dropped rather than parked in the inbox, where it would be
// consumed with no creator check once the task is scheduled.
func Test_addWorkflowEvent_crossAppResultCreatorCheck(t *testing.T) {
	t.Parallel()

	other, appB := "other", "appB"

	tests := map[string]struct {
		scheduled *backend.HistoryEvent
		sender    string
		admitted  bool
	}{
		"another app for an unscheduled task is dropped":       {sender: other},
		"own app for an unscheduled task keeps the inbox":      {sender: "testapp", admitted: true},
		"unknown creator for an unscheduled task is kept":      {sender: "", admitted: true},
		"the dispatch target is admitted":                      {scheduled: taskScheduledEvent(9, &other), sender: other, admitted: true},
		"another app than the dispatch target is dropped":      {scheduled: taskScheduledEvent(9, &appB), sender: other},
		"another app for a locally dispatched task is dropped": {scheduled: taskScheduledEvent(9, nil), sender: other},
		"own app for a remotely dispatched task is admitted":   {scheduled: taskScheduledEvent(9, &appB), sender: "testapp", admitted: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			o, saves := newForgedOrchestrator(t, "test-creator")
			if tc.scheduled != nil {
				o.state.AddToHistory(tc.scheduled)
			}
			o.activityResultAwaited.Store(true)

			require.NoError(t, o.addWorkflowEvent(t.Context(), taskCompletedEvent(9), tc.sender), "a drop is acked")
			if tc.admitted {
				assert.Len(t, o.state.Inbox, 1)
				assert.Equal(t, int32(1), saves.Load())
				assert.False(t, o.activityResultAwaited.Load(), "an admitted result settles the await")
			} else {
				assert.Empty(t, o.state.Inbox)
				assert.Zero(t, saves.Load())
				assert.True(t, o.activityResultAwaited.Load(), "a forged result must not release the reuse guard")
			}
		})
	}
}

// The creator verified by the Scheduler reaches the creator check through
// the activity-result reminder.
func Test_handleReminder_activityResultChecksCreator(t *testing.T) {
	t.Parallel()

	appB := "appB"
	o, saves := newForgedOrchestrator(t, "test-reminder-creator")
	o.state.AddToHistory(taskScheduledEvent(9, &appB))

	data, err := anypb.New(taskCompletedEvent(9))
	require.NoError(t, err)
	reminder := &actorapi.Reminder{Name: "activity-result-x", ActorType: o.actorType, ActorID: o.actorID, Data: data}

	reminder.SourceAppID = "evil"
	require.NoError(t, o.handleReminder(t.Context(), reminder))
	assert.Empty(t, o.state.Inbox)
	assert.Zero(t, saves.Load())

	reminder.SourceAppID = appB
	require.NoError(t, o.handleReminder(t.Context(), reminder))
	assert.Len(t, o.state.Inbox, 1)
}
