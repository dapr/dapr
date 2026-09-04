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

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/timestamppb"

	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

func testState(t *testing.T) *wfenginestate.State {
	t.Helper()
	return wfenginestate.NewState(wfenginestate.Options{
		AppID:             "testapp",
		Namespace:         "default",
		WorkflowActorType: "dapr.internal.default.testapp.workflow",
		ActivityActorType: "dapr.internal.default.testapp.activity",
	})
}

func startedEvent() *backend.HistoryEvent {
	return &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{Name: "TestWorkflow"},
		},
	}
}

func taskScheduledEvent(id int32) *backend.HistoryEvent {
	return &protos.HistoryEvent{
		EventId:   id,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskScheduled{
			TaskScheduled: &protos.TaskScheduledEvent{Name: "act"},
		},
	}
}

func taskFailedEvent(scheduledID int32) *backend.HistoryEvent {
	return &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskFailed{
			TaskFailed: &protos.TaskFailedEvent{TaskScheduledId: scheduledID},
		},
	}
}

func Test_unresolvedScheduledTasks(t *testing.T) {
	t.Parallel()

	ids := func(events []*backend.HistoryEvent) []int32 {
		out := make([]int32, 0, len(events))
		for _, e := range events {
			out = append(out, e.GetEventId())
		}
		return out
	}

	t.Run("no scheduled tasks", func(t *testing.T) {
		state := testState(t)
		state.AddToHistory(startedEvent())
		assert.Empty(t, unresolvedScheduledTasks(state, nil))
	})

	t.Run("pending task is unresolved", func(t *testing.T) {
		state := testState(t)
		state.AddToHistory(startedEvent())
		state.AddToHistory(taskScheduledEvent(1))
		assert.Equal(t, []int32{1}, ids(unresolvedScheduledTasks(state, nil)))
	})

	t.Run("resolution in history", func(t *testing.T) {
		state := testState(t)
		state.AddToHistory(startedEvent())
		state.AddToHistory(taskScheduledEvent(1))
		state.AddToHistory(taskCompletedEvent(1))
		assert.Empty(t, unresolvedScheduledTasks(state, nil))
	})

	t.Run("resolution pending in inbox", func(t *testing.T) {
		state := testState(t)
		state.AddToHistory(startedEvent())
		state.AddToHistory(taskScheduledEvent(1))
		state.AddToInbox(taskCompletedEvent(1))
		assert.Empty(t, unresolvedScheduledTasks(state, nil))
	})

	t.Run("failure counts as resolution", func(t *testing.T) {
		state := testState(t)
		state.AddToHistory(startedEvent())
		state.AddToHistory(taskScheduledEvent(1))
		state.AddToHistory(taskFailedEvent(1))
		assert.Empty(t, unresolvedScheduledTasks(state, nil))
	})

	t.Run("mixed: only the unresolved task is returned", func(t *testing.T) {
		state := testState(t)
		state.AddToHistory(startedEvent())
		state.AddToHistory(taskScheduledEvent(1))
		state.AddToHistory(taskScheduledEvent(2))
		state.AddToHistory(taskScheduledEvent(3))
		state.AddToHistory(taskCompletedEvent(1))
		state.AddToInbox(taskFailedEvent(3))
		assert.Equal(t, []int32{2}, ids(unresolvedScheduledTasks(state, nil)))
	})

	t.Run("timers and child workflows are not scheduled tasks", func(t *testing.T) {
		state := testState(t)
		state.AddToHistory(startedEvent())
		state.AddToHistory(&protos.HistoryEvent{
			EventId:   5,
			Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_TimerCreated{
				TimerCreated: &protos.TimerCreatedEvent{FireAt: timestamppb.Now()},
			},
		})
		state.AddToHistory(&protos.HistoryEvent{
			EventId:   6,
			Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_ChildWorkflowInstanceCreated{
				ChildWorkflowInstanceCreated: &protos.ChildWorkflowInstanceCreatedEvent{Name: "child"},
			},
		})
		assert.Empty(t, unresolvedScheduledTasks(state, nil))
	})

	t.Run("timer resolution does not resolve a task with the same id", func(t *testing.T) {
		state := testState(t)
		state.AddToHistory(startedEvent())
		state.AddToHistory(taskScheduledEvent(4))
		state.AddToHistory(&protos.HistoryEvent{
			EventId:   -1,
			Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_TimerFired{
				TimerFired: &protos.TimerFiredEvent{TimerId: 4},
			},
		})
		assert.Equal(t, []int32{4}, ids(unresolvedScheduledTasks(state, nil)))
	})
}
