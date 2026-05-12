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

package events

import (
	"fmt"

	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

// NewEventReminderName builds a deterministic name for the wake-up reminder
// that drains the workflow inbox after an event is appended. Retries of the
// same inbox append must collapse onto a single scheduler entry (the scheduler
// overwrites by name) instead of accumulating under random suffixes.
func NewEventReminderName(prefix string, e *backend.HistoryEvent) string {
	switch evt := e.GetEventType().(type) {
	case *protos.HistoryEvent_TaskCompleted:
		return fmt.Sprintf("%s-tc-%d", prefix, evt.TaskCompleted.GetTaskScheduledId())
	case *protos.HistoryEvent_TaskFailed:
		return fmt.Sprintf("%s-tf-%d", prefix, evt.TaskFailed.GetTaskScheduledId())
	case *protos.HistoryEvent_ChildWorkflowInstanceCompleted:
		return fmt.Sprintf("%s-cwc-%d", prefix, evt.ChildWorkflowInstanceCompleted.GetTaskScheduledId())
	case *protos.HistoryEvent_ChildWorkflowInstanceFailed:
		return fmt.Sprintf("%s-cwf-%d", prefix, evt.ChildWorkflowInstanceFailed.GetTaskScheduledId())
	case *protos.HistoryEvent_EventRaised:
		return fmt.Sprintf("%s-er-%s", prefix, evt.EventRaised.GetName())
	case *protos.HistoryEvent_TimerFired:
		return fmt.Sprintf("%s-tmf-%d", prefix, evt.TimerFired.GetTimerId())
	case *protos.HistoryEvent_ExecutionStarted:
		return fmt.Sprintf("%s-es-%d", prefix, e.GetTimestamp().AsTime().UnixNano())
	case *protos.HistoryEvent_ExecutionTerminated:
		return fmt.Sprintf("%s-et-%d", prefix, e.GetTimestamp().AsTime().UnixNano())
	case *protos.HistoryEvent_ExecutionSuspended:
		return fmt.Sprintf("%s-esp-%d", prefix, e.GetTimestamp().AsTime().UnixNano())
	case *protos.HistoryEvent_ExecutionResumed:
		return fmt.Sprintf("%s-erm-%d", prefix, e.GetTimestamp().AsTime().UnixNano())
	default:
		return fmt.Sprintf("%s-x-%d", prefix, e.GetTimestamp().AsTime().UnixNano())
	}
}
