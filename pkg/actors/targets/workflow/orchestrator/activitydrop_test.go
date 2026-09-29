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

func dropState(events ...*backend.HistoryEvent) *wfenginestate.State {
	return &wfenginestate.State{History: events}
}

func taskScheduled(id int32, execID string) *backend.HistoryEvent {
	return &protos.HistoryEvent{
		EventId:   id,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskScheduled{
			TaskScheduled: &protos.TaskScheduledEvent{Name: "a", TaskExecutionId: execID},
		},
	}
}

func executionCompleted(id int32) *backend.HistoryEvent {
	return &protos.HistoryEvent{
		EventId:   id,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionCompleted{
			ExecutionCompleted: &protos.ExecutionCompletedEvent{},
		},
	}
}

func taskCompleted(taskID int32, execID string) *backend.HistoryEvent {
	return &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{TaskScheduledId: taskID, TaskExecutionId: execID},
		},
	}
}

// drop runs activityDrop the way addWorkflowEvent does, paying the history
// lookup once.
func drop(t *testing.T, state *wfenginestate.State, e *backend.HistoryEvent) string {
	t.Helper()
	taskID, execID, isResolution := activityResolution(e)
	return activityDrop(state, taskID, execID, isResolution, state.FindHistoryEventByID(taskID).GetTaskScheduled())
}

func Test_activityDrop(t *testing.T) {
	t.Parallel()

	t.Run("the current scheduling's own result is consumable", func(t *testing.T) {
		t.Parallel()
		st := dropState(taskScheduled(7, "exec-B"))
		assert.Empty(t, drop(t, st, taskCompleted(7, "exec-B")))
	})

	t.Run("a superseded scheduling is dropped", func(t *testing.T) {
		t.Parallel()
		// ContinueAsNew resets task ids, so an orphan of the previous
		// generation resolves the same id under a different execution.
		st := dropState(taskScheduled(7, "exec-B"))
		assert.Contains(t, drop(t, st, taskCompleted(7, "exec-A")), "superseded scheduling of task 7")
	})

	t.Run("senders without an execution id still match by task id", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, drop(t, dropState(taskScheduled(7, "exec-B")), taskCompleted(7, "")))
		assert.Empty(t, drop(t, dropState(taskScheduled(7, "")), taskCompleted(7, "exec-B")))
	})

	t.Run("an id this generation passed without scheduling is dropped", func(t *testing.T) {
		t.Parallel()
		st := dropState(taskScheduled(7, "exec-B"))
		assert.Contains(t, drop(t, st, taskCompleted(3, "exec-A")), "passed id 3 without scheduling a task")
	})

	t.Run("an id above every recorded one may still be committing", func(t *testing.T) {
		t.Parallel()
		// Dispatch precedes the save, so the scheduling row for a higher id
		// can still be in flight: this is not proof it can never be consumed.
		st := dropState(taskScheduled(7, "exec-B"))
		assert.Empty(t, drop(t, st, taskCompleted(8, "exec-C")))
	})

	t.Run("a completed workflow consumes nothing", func(t *testing.T) {
		t.Parallel()
		st := dropState(taskScheduled(7, "exec-B"), executionCompleted(8))
		assert.Equal(t, "the workflow has completed", drop(t, st, taskCompleted(7, "exec-B")))
	})

	t.Run("non-activity events are never dropped here", func(t *testing.T) {
		t.Parallel()
		st := dropState(taskScheduled(7, "exec-B"), executionCompleted(8))
		raised := &protos.HistoryEvent{
			EventId:   -1,
			Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_EventRaised{EventRaised: &protos.EventRaisedEvent{Name: "go"}},
		}
		assert.Empty(t, drop(t, st, raised))
	})
}
