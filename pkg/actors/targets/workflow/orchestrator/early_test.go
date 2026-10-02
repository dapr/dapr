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

	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

func timerCreatedEvent(id int32) *backend.HistoryEvent {
	return &protos.HistoryEvent{EventId: id, EventType: &protos.HistoryEvent_TimerCreated{TimerCreated: &protos.TimerCreatedEvent{}}}
}

func childCreatedEvent(id int32) *backend.HistoryEvent {
	return &protos.HistoryEvent{EventId: id, EventType: &protos.HistoryEvent_ChildWorkflowInstanceCreated{
		ChildWorkflowInstanceCreated: &protos.ChildWorkflowInstanceCreatedEvent{InstanceId: "child"},
	}}
}

func firedEvent(id int32) *backend.HistoryEvent {
	return &protos.HistoryEvent{EventId: -1, EventType: &protos.HistoryEvent_TimerFired{TimerFired: &protos.TimerFiredEvent{TimerId: id}}}
}

func TestUnscheduledLast(t *testing.T) {
	t.Parallel()

	history := []*backend.HistoryEvent{startedEvent(), taskScheduledEvent(0)}
	scheduledResult := taskCompletedWithExecID(0, "")
	early := taskCompletedWithExecID(1, "")
	earlyChild := childCompletedEvent(2)
	earlyTimer := firedEvent(3)
	other := &protos.HistoryEvent{EventId: -1, EventType: &protos.HistoryEvent_EventRaised{EventRaised: &protos.EventRaisedEvent{Name: "go"}}}

	t.Run("resolutions of unscheduled steps go last, in order", func(t *testing.T) {
		t.Parallel()
		got := unscheduledLast(history, []*backend.HistoryEvent{early, scheduledResult, earlyChild, other, earlyTimer})
		assert.Equal(t, []*backend.HistoryEvent{scheduledResult, other, early, earlyChild, earlyTimer}, got)
	})

	t.Run("events are returned as they are when every step is scheduled", func(t *testing.T) {
		t.Parallel()
		events := []*backend.HistoryEvent{scheduledResult, other}
		got := unscheduledLast(history, events)
		assert.Equal(t, events, got)
		assert.Same(t, &events[0], &got[0], "nothing to move: the slice is not copied")
	})

	t.Run("a scheduling of another kind does not count", func(t *testing.T) {
		t.Parallel()
		h := []*backend.HistoryEvent{startedEvent(), timerCreatedEvent(0)}
		got := unscheduledLast(h, []*backend.HistoryEvent{scheduledResult, other})
		assert.Equal(t, []*backend.HistoryEvent{other, scheduledResult}, got)
	})
}

func TestStripUnmatchedResolutions(t *testing.T) {
	t.Parallel()

	t.Run("keeps an early activity or child result and drops an early timer firing", func(t *testing.T) {
		t.Parallel()
		o := &orchestrator{actorID: "wf"}
		state := testState(t)
		state.AddToHistory(startedEvent())
		state.AddToHistory(taskScheduledEvent(0))
		matched := taskCompletedWithExecID(0, "")
		early := taskCompletedWithExecID(1, "")
		earlyChild := childCompletedEvent(2)
		earlyTimer := firedEvent(3)
		rs := &backend.WorkflowRuntimeState{NewEvents: []*backend.HistoryEvent{matched, early, earlyChild, earlyTimer}}

		kept := o.stripUnmatchedResolutions(state, rs)
		assert.Equal(t, []*backend.HistoryEvent{early, earlyChild}, kept)
		assert.Equal(t, []*backend.HistoryEvent{matched}, rs.GetNewEvents(), "no unmatched resolution is saved into history")
	})

	t.Run("a resolution the turn schedules is matched", func(t *testing.T) {
		t.Parallel()
		o := &orchestrator{actorID: "wf"}
		state := testState(t)
		state.AddToHistory(startedEvent())
		early := taskCompletedWithExecID(1, "")
		earlyChild := childCompletedEvent(2)
		earlyTimer := firedEvent(3)
		rs := &backend.WorkflowRuntimeState{NewEvents: []*backend.HistoryEvent{
			taskScheduledEvent(1), childCreatedEvent(2), timerCreatedEvent(3), early, earlyChild, earlyTimer,
		}}

		kept := o.stripUnmatchedResolutions(state, rs)
		assert.Empty(t, kept)
		assert.Len(t, rs.GetNewEvents(), 6)
	})

	t.Run("drops a result whose event ID the history has reached", func(t *testing.T) {
		t.Parallel()
		o := &orchestrator{actorID: "wf"}
		state := testState(t)
		state.AddToHistory(startedEvent())
		state.AddToHistory(taskScheduledEvent(0))
		state.AddToHistory(timerCreatedEvent(1))
		state.AddToHistory(taskScheduledEvent(2))
		straggler := taskCompletedWithExecID(1, "")
		rs := &backend.WorkflowRuntimeState{NewEvents: []*backend.HistoryEvent{straggler}}

		assert.Empty(t, o.stripUnmatchedResolutions(state, rs), "event 1 is a timer: the result can never be consumed")
		assert.Empty(t, rs.GetNewEvents())
	})

	t.Run("keeps nothing for a turn that completed or continued as new", func(t *testing.T) {
		t.Parallel()
		for name, rs := range map[string]*backend.WorkflowRuntimeState{
			"completed":        {CompletedEvent: &protos.ExecutionCompletedEvent{}},
			"continued as new": {ContinuedAsNew: true},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				o := &orchestrator{actorID: "wf"}
				state := testState(t)
				state.AddToHistory(startedEvent())
				rs.NewEvents = []*backend.HistoryEvent{taskCompletedWithExecID(1, "")}
				assert.Empty(t, o.stripUnmatchedResolutions(state, rs))
				assert.Empty(t, rs.GetNewEvents())
			})
		}
	})
}
