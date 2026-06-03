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
	"errors"

	"github.com/dapr/durabletask-go/backend"
)

// reminderNameDispatchRetry is the deterministic per-workflow safety
// reminder created when an activity dispatch fails AFTER the workflow's
// state was already persisted with the TaskScheduled event. The reminder
// fires on a backoff, scans state for any TaskScheduled whose result is
// not yet in history or inbox, and re-attempts dispatch. Idempotent by
// design: the activity actor's run-activity reminder is upserted under a
// fixed name, so re-dispatching an in-flight activity is a no-op.
const reminderNameDispatchRetry = "dispatch-retry"

type dispatchResult struct {
	failedEventIDs map[int32]struct{}
	err            error
}

func (r *dispatchResult) recordFailure(eventID int32, err error) {
	if r.failedEventIDs == nil {
		r.failedEventIDs = make(map[int32]struct{})
	}
	r.failedEventIDs[eventID] = struct{}{}
	r.err = errors.Join(r.err, err)
}

// hasRemoteTasks reports whether any of the given TaskScheduled events
// targets a remote application. Used to detect when the legacy
// "framing-only first-execution save" path (PR #9738) must be preserved
// instead of save-before-dispatch.
func hasRemoteTasks(es []*backend.HistoryEvent) bool {
	for _, e := range es {
		if router := e.GetRouter(); router != nil && router.TargetAppID != nil {
			return true
		}
	}
	return false
}

// hasRemoteMessages reports whether any of the given outbound workflow
// state messages targets a remote application.
func hasRemoteMessages(msgs []*backend.WorkflowRuntimeStateMessage) bool {
	for _, msg := range msgs {
		if router := msg.GetHistoryEvent().GetRouter(); router != nil && router.TargetAppID != nil {
			return true
		}
	}
	return false
}

// isDispatchableEvent returns true for events that, when present in
// rs.NewEvents on first execution, must be filtered out of the saved
// history when their dispatch failed against an unreachable remote app
// (so the next reminder fire re-emits and re-attempts them).
func isDispatchableEvent(e *backend.HistoryEvent) bool {
	return e.GetTaskScheduled() != nil || e.GetChildWorkflowInstanceCreated() != nil
}
