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

package fork

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

func TestBuildTimers(t *testing.T) {
	t.Parallel()

	t.Run("a timer that fired before the target is kept, not rerun",
		func(t *testing.T) {
			t.Parallel()
			started, created, raised, fired, target := startedEvent(), timerCreatedEvent(0), raisedEvent(), timerFiredEvent(0), taskScheduledEvent(1)

			forked := build(t, 1, started, created, raised, fired, target)

			// Its creation moves to just before the firing, as an activity's
			// scheduling moves to its result.
			assert.Equal(t, []*backend.HistoryEvent{started, raised, created, fired}, forked.History)
			assert.Equal(t, []*backend.HistoryEvent{target}, forked.Inbox)
		})

	t.Run("a timer still running at the target is rerun",
		func(t *testing.T) {
			t.Parallel()
			started, created, target := startedEvent(), timerCreatedEvent(0), taskScheduledEvent(1)

			forked := build(t, 1, started, created, target)

			assert.Equal(t, []*backend.HistoryEvent{started}, forked.History)
			assert.Equal(t, []*backend.HistoryEvent{created, target}, forked.Inbox)
		})

	t.Run("a firing with no timer before it stays where it is",
		func(t *testing.T) {
			t.Parallel()
			started, fired, created, target := startedEvent(), timerFiredEvent(0), timerCreatedEvent(0), taskScheduledEvent(1)

			forked := build(t, 1, started, fired, created, target)

			assert.Equal(t, []*backend.HistoryEvent{started, fired}, forked.History)
			assert.Equal(t, []*backend.HistoryEvent{created, target}, forked.Inbox)
		})
}

func startedEvent() *backend.HistoryEvent {
	return &protos.HistoryEvent{EventId: -1, EventType: &protos.HistoryEvent_ExecutionStarted{
		ExecutionStarted: &protos.ExecutionStartedEvent{Name: "wf"},
	}}
}

func raisedEvent() *backend.HistoryEvent {
	return &protos.HistoryEvent{EventId: -1, EventType: &protos.HistoryEvent_EventRaised{
		EventRaised: &protos.EventRaisedEvent{Name: "go"},
	}}
}

func timerCreatedEvent(id int32) *backend.HistoryEvent {
	return &protos.HistoryEvent{EventId: id, EventType: &protos.HistoryEvent_TimerCreated{
		TimerCreated: &protos.TimerCreatedEvent{},
	}}
}

func timerFiredEvent(id int32) *backend.HistoryEvent {
	return &protos.HistoryEvent{EventId: -1, EventType: &protos.HistoryEvent_TimerFired{
		TimerFired: &protos.TimerFiredEvent{TimerId: id},
	}}
}

func taskScheduledEvent(id int32) *backend.HistoryEvent {
	return &protos.HistoryEvent{EventId: id, EventType: &protos.HistoryEvent_TaskScheduled{
		TaskScheduled: &protos.TaskScheduledEvent{Name: "act"},
	}}
}

// build forks history at the target event.
func build(t *testing.T, target int32, history ...*backend.HistoryEvent) *state.State {
	t.Helper()

	old := state.NewState(state.Options{})
	for _, e := range history {
		old.AddToHistory(e)
	}

	forked, err := New(Options{
		InstanceID:    "abc",
		NewInstanceID: "xyz",
		TargetEventID: target,
		OldState:      old,
	}).Build()
	require.NoError(t, err)

	return forked
}

func TestBuildChildAsIndependentWorkflow(t *testing.T) {
	old := state.NewState(state.Options{})
	started := startedEvent()
	started.GetExecutionStarted().ParentInstance = &protos.ParentInstanceInfo{
		TaskScheduledId:  4,
		WorkflowInstance: &protos.WorkflowInstance{InstanceId: "parent"},
	}
	old.AddToHistory(started)
	old.AddToHistory(taskScheduledEvent(0))

	// Repeated forks must detach only the copied start event; the source is
	// still a child, including when another rerun is requested on this host.
	for _, id := range []string{"first", "second"} {
		forked, err := New(Options{
			InstanceID: "parent:0004", NewInstanceID: id, OldState: old,
			TargetEventID: 0,
		}).Build()
		require.NoError(t, err)
		require.Len(t, forked.History, 1)
		assert.Nil(t, forked.History[0].GetExecutionStarted().GetParentInstance())
		require.NotNil(t, started.GetExecutionStarted().GetParentInstance())
		assert.Equal(t, "parent", started.GetExecutionStarted().GetParentInstance().GetWorkflowInstance().GetInstanceId())
		assert.Equal(t, int32(4), started.GetExecutionStarted().GetParentInstance().GetTaskScheduledId())
	}
}
