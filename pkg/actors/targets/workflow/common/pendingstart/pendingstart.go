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

// Package pendingstart describes a workflow instance whose creation was
// committed but which has never executed a turn.
package pendingstart

import (
	"sync"
	"time"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/backend"
)

// defaultRedriveGrace is how long a pending start may be overdue before a
// status read re-asserts its start reminder. Re-asserting an armed reminder
// is a harmless overwrite-by-name, so it only needs to exceed the Scheduler's
// normal trigger latency.
const defaultRedriveGrace = 5 * time.Second

// RedriveGrace resolves the overdue grace once per process.
var RedriveGrace = sync.OnceValue(func() time.Duration {
	return common.EnvDurationOr("DAPR_WORKFLOW_PENDING_START_REDRIVE_GRACE", defaultRedriveGrace)
})

// Event returns the ExecutionStarted inbox event of an instance with an empty
// history, or nil. Other inbox rows (a RaiseEvent against a pending instance)
// are ignored.
func Event(state *wfenginestate.State) *backend.HistoryEvent {
	if state == nil || len(state.History) > 0 {
		return nil
	}
	for _, e := range state.Inbox {
		if e.GetExecutionStarted() != nil {
			return e
		}
	}
	return nil
}

// DueTime is when the start reminder for an ExecutionStarted event is due:
// its scheduled start when set, else the event timestamp.
func DueTime(startEvent *backend.HistoryEvent) time.Time {
	if ts := startEvent.GetExecutionStarted().GetScheduledStartTimestamp(); ts != nil {
		return ts.AsTime()
	}
	return startEvent.GetTimestamp().AsTime()
}

// Overdue returns the pending start whose due time passed more than the grace
// ago, or nil.
func Overdue(state *wfenginestate.State, now time.Time) *backend.HistoryEvent {
	pending := Event(state)
	if pending == nil || now.Before(DueTime(pending).Add(RedriveGrace())) {
		return nil
	}
	return pending
}
