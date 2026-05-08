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

package dedup_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/dedup"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

func taskCompleted(id int32) *backend.HistoryEvent {
	return &backend.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{TaskScheduledId: id},
		},
	}
}

func taskFailed(id int32) *backend.HistoryEvent {
	return &backend.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskFailed{
			TaskFailed: &protos.TaskFailedEvent{TaskScheduledId: id},
		},
	}
}

func timerFired(id int32) *backend.HistoryEvent {
	return &backend.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TimerFired{
			TimerFired: &protos.TimerFiredEvent{TimerId: id},
		},
	}
}

func childCompleted(id int32) *backend.HistoryEvent {
	return &backend.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ChildWorkflowInstanceCompleted{
			ChildWorkflowInstanceCompleted: &protos.ChildWorkflowInstanceCompletedEvent{
				TaskScheduledId: id,
			},
		},
	}
}

func eventRaised(name string) *backend.HistoryEvent {
	return &backend.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_EventRaised{
			EventRaised: &protos.EventRaisedEvent{Name: name},
		},
	}
}

func taskScheduled(eventID int32, name string) *backend.HistoryEvent {
	return &backend.HistoryEvent{
		EventId:   eventID,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskScheduled{
			TaskScheduled: &protos.TaskScheduledEvent{Name: name},
		},
	}
}

func TestIsDuplicateCompletion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		event   *backend.HistoryEvent
		history []*backend.HistoryEvent
		inbox   []*backend.HistoryEvent
		want    bool
	}{
		{name: "no history or inbox", event: taskCompleted(1), want: false},
		{name: "match in history", event: taskCompleted(1), history: []*backend.HistoryEvent{taskCompleted(1)}, want: true},
		{name: "match in inbox", event: taskCompleted(2), inbox: []*backend.HistoryEvent{taskCompleted(2)}, want: true},
		{name: "TaskFailed for same id as TaskCompleted in history", event: taskFailed(3), history: []*backend.HistoryEvent{taskCompleted(3)}, want: true},
		{name: "different id", event: taskCompleted(2), history: []*backend.HistoryEvent{taskCompleted(1)}, want: false},
		{name: "TimerFired matched", event: timerFired(7), history: []*backend.HistoryEvent{timerFired(7)}, want: true},
		{name: "Timer id same as Task id is not a match", event: timerFired(1), history: []*backend.HistoryEvent{taskCompleted(1)}, want: false},
		{name: "ChildWorkflowInstanceCompleted matched", event: childCompleted(4), history: []*backend.HistoryEvent{childCompleted(4)}, want: true},
		{name: "EventRaised never deduped here", event: eventRaised("approval"), history: []*backend.HistoryEvent{eventRaised("approval")}, want: false},
		{name: "inbox match wins over history mismatch", event: taskCompleted(5), history: []*backend.HistoryEvent{taskCompleted(99)}, inbox: []*backend.HistoryEvent{taskCompleted(5)}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, dedup.IsDuplicateCompletion(tt.event, tt.history, tt.inbox))
		})
	}
}

func TestIsTaskAlreadyResolved(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		scheduled *backend.HistoryEvent
		history   []*backend.HistoryEvent
		inbox     []*backend.HistoryEvent
		want      bool
	}{
		{name: "nothing recorded", scheduled: taskScheduled(1, "fetch_demographics"), want: false},
		{name: "TaskCompleted in history", scheduled: taskScheduled(1, "fetch_demographics"), history: []*backend.HistoryEvent{taskCompleted(1)}, want: true},
		{name: "TaskFailed in history is also a resolution", scheduled: taskScheduled(2, "get_bill"), history: []*backend.HistoryEvent{taskFailed(2)}, want: true},
		{name: "resolution in inbox still counts", scheduled: taskScheduled(3, "check_unit_charges"), inbox: []*backend.HistoryEvent{taskCompleted(3)}, want: true},
		{name: "resolution exists for a different id", scheduled: taskScheduled(4, "publish_agent_completed"), history: []*backend.HistoryEvent{taskCompleted(99)}, want: false},
		{name: "non-TaskScheduled input returns false", scheduled: taskCompleted(1), history: []*backend.HistoryEvent{taskCompleted(1)}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, dedup.IsTaskAlreadyResolved(tt.scheduled, tt.history, tt.inbox))
		})
	}
}
