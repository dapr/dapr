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
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/events"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
)

func timerFiredEvent(id int32) *backend.HistoryEvent {
	return &backend.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TimerFired{TimerFired: &protos.TimerFiredEvent{TimerId: id}},
	}
}

func TestWithoutUnscheduledResults(t *testing.T) {
	t.Parallel()

	state := wfenginestate.NewState(wfenginestate.Options{})
	state.History = []*backend.HistoryEvent{
		{EventId: -1, EventType: &protos.HistoryEvent_ExecutionStarted{ExecutionStarted: &protos.ExecutionStartedEvent{Name: "wf"}}},
		scheduledWithExecution(1, ""),
		{EventId: 2, EventType: &protos.HistoryEvent_TimerCreated{TimerCreated: &protos.TimerCreatedEvent{}}},
	}
	raised := &backend.HistoryEvent{EventId: -1, EventType: &protos.HistoryEvent_EventRaised{EventRaised: &protos.EventRaisedEvent{Name: "e"}}}
	scheduledResult := completedWithExecution(1, "")
	fired := timerFiredEvent(2)
	state.Inbox = []*backend.HistoryEvent{
		completedWithExecution(3, "run:a3"), // its scheduling was never saved
		fired,
		scheduledResult,
		failedWithExecution(9, ""), // nor this one's
		raised,
	}

	o := &orchestrator{actorID: "wf"}
	got, held := o.withoutUnscheduledResults(state)
	assert.Equal(t, []*backend.HistoryEvent{fired, scheduledResult, raised}, got)
	assert.Equal(t, []*backend.HistoryEvent{state.Inbox[0], state.Inbox[3]}, held)
	assert.Len(t, state.Inbox, 5, "the persisted inbox is left as it is")

	state.Inbox = []*backend.HistoryEvent{fired, scheduledResult}
	got, held = o.withoutUnscheduledResults(state)
	assert.Equal(t, state.Inbox, got, "an inbox with nothing to hold back is returned as it is")
	assert.Empty(t, held)
}

func TestRescheduledResults(t *testing.T) {
	t.Parallel()
	same := completedWithExecution(3, "run:a3")
	bothEmpty := failedWithExecution(4, "")
	otherExec := completedWithExecution(5, "run-old:a5")
	notScheduled := completedWithExecution(6, "run:a6")
	newEvents := []*backend.HistoryEvent{
		scheduledWithExecution(3, "run:a3"),
		scheduledWithExecution(4, ""),
		scheduledWithExecution(5, "run:a5"),
	}
	got := rescheduledResults([]*backend.HistoryEvent{same, bothEmpty, otherExec, notScheduled}, newEvents)
	assert.Equal(t, []*backend.HistoryEvent{same, bothEmpty}, got)
	assert.Equal(t, newEvents[2:], withoutTasks(newEvents, got))
}

// Test_runWorkflow_carriesResultOfUnsavedScheduling reproduces a turn
// retried after its save failed: the failed turn had dispatched task 3, the
// activity ran and its result reached the inbox, but the TaskScheduled for
// task 3 was never saved. The retried turn must not hand the workflow that
// result before the task exists, must not dispatch the task again (the
// activity actor would only return its cached outcome, never publishing a
// second result), and must keep the result, so the turn after it delivers it
// and the task resolves.
func Test_runWorkflow_carriesResultOfUnsavedScheduling(t *testing.T) {
	t.Parallel()

	startEvent := &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{
				Name:             "TestWorkflow",
				Input:            wrapperspb.String(`null`),
				WorkflowInstance: &protos.WorkflowInstance{InstanceId: notifyChildID},
			},
		},
	}
	history := []*backend.HistoryEvent{
		{EventId: -1, Timestamp: timestamppb.Now(), EventType: &protos.HistoryEvent_WorkflowStarted{WorkflowStarted: &protos.WorkflowStartedEvent{}}},
		startEvent,
		scheduledWithExecution(1, "run:a1"),
		{EventId: 2, Timestamp: timestamppb.Now(), EventType: &protos.HistoryEvent_TimerCreated{TimerCreated: &protos.TimerCreatedEvent{}}},
	}
	result := completedWithExecution(3, "run:a3")
	fired := timerFiredEvent(2)

	var turns [][]*backend.HistoryEvent
	scheduler := func(_ context.Context, wi *backend.WorkflowWorkItem) error {
		turns = append(turns, append([]*backend.HistoryEvent(nil), wi.NewEvents...))
		_ = runtimestate.AddEvent(wi.State, &backend.HistoryEvent{EventId: -1, Timestamp: timestamppb.Now(), EventType: &protos.HistoryEvent_WorkflowStarted{WorkflowStarted: &protos.WorkflowStartedEvent{}}})
		for _, e := range wi.NewEvents {
			_ = runtimestate.AddEvent(wi.State, e)
		}
		if len(turns) == 1 {
			// The timer fired: the workflow schedules task 3 again, under
			// the same execution id as the dispatch whose save failed.
			scheduled := scheduledWithExecution(3, "run:a3")
			_ = runtimestate.AddEvent(wi.State, scheduled)
			wi.State.PendingTasks = append(wi.State.PendingTasks, scheduled)
		}
		wi.Properties[todo.CallbackChannelProperty].(chan bool) <- true
		return nil
	}

	h := newNotifyHarness(t, history, nil, false, scheduler)
	state := wfenginestate.NewState(wfenginestate.Options{
		AppID:             "testapp",
		Namespace:         "default",
		WorkflowActorType: "dapr.internal.default.testapp.workflow",
		ActivityActorType: "dapr.internal.default.testapp.activity",
	})
	for _, e := range history {
		state.AddToHistory(e)
	}
	state.AddToInbox(result)
	state.AddToInbox(fired)
	h.orch.state = state
	h.orch.rstate = runtimestate.NewWorkflowRuntimeState(notifyChildID, nil, history)
	h.orch.ometa = h.orch.ometaFromState(h.orch.rstate, startEvent.GetExecutionStarted())

	_, err := h.orch.runWorkflow(t.Context(), &actorapi.Reminder{Name: "new-event-test"})
	require.NoError(t, err)
	require.Len(t, turns, 1)
	assert.Equal(t, []*backend.HistoryEvent{fired}, turns[0], "the result of a task whose scheduling was never saved must not reach the workflow before the task exists")
	assert.Equal(t, []*backend.HistoryEvent{result}, h.orch.state.Inbox, "the result is kept in the inbox, saved with its task's scheduling")
	assert.NotContains(t, h.snapshot(), "call:"+todo.ExecuteActivityMethod, "the activity already ran; it is not dispatched again")
	assert.Contains(t, h.snapshot(), "create:"+events.EventReminderName(reminderPrefixNewEvent, result), "a turn is armed for the kept result")

	_, err = h.orch.runWorkflow(t.Context(), &actorapi.Reminder{Name: events.EventReminderName(reminderPrefixNewEvent, result)})
	require.NoError(t, err)
	require.Len(t, turns, 2)
	assert.Equal(t, []*backend.HistoryEvent{result}, turns[1], "the next turn delivers the result after its task's scheduling")
	assert.Empty(t, h.orch.state.Inbox)
	resolved := false
	for _, e := range h.orch.state.History {
		if e.GetTaskCompleted().GetTaskScheduledId() == 3 {
			resolved = true
		}
	}
	assert.True(t, resolved, "task 3 is resolved in history")
}

func scheduledWithExecution(id int32, execID string) *backend.HistoryEvent {
	return &backend.HistoryEvent{
		EventId: id,
		EventType: &protos.HistoryEvent_TaskScheduled{
			TaskScheduled: &protos.TaskScheduledEvent{Name: "act", TaskExecutionId: execID},
		},
	}
}

func completedWithExecution(id int32, execID string) *backend.HistoryEvent {
	return &backend.HistoryEvent{
		EventId: -1,
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{TaskScheduledId: id, TaskExecutionId: execID, Result: wrapperspb.String("r")},
		},
	}
}

func failedWithExecution(id int32, execID string) *backend.HistoryEvent {
	return &backend.HistoryEvent{
		EventId: -1,
		EventType: &protos.HistoryEvent_TaskFailed{
			TaskFailed: &protos.TaskFailedEvent{TaskScheduledId: id, TaskExecutionId: execID},
		},
	}
}
