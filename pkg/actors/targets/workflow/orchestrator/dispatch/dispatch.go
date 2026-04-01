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

package dispatch

import (
	"errors"
	"time"

	"github.com/dapr/durabletask-go/backend"
)

// Timeout is the timeout for dispatching activities and child workflows to
// their target actor. Dispatch is a one-way fire-and-forget message, so it
// should complete in milliseconds when the target app is reachable. If the app
// is offline, the short timeout ensures the actor lock is released quickly so
// status queries and reminder retries can proceed.
const Timeout = 2 * time.Second

// Result holds the outcome of dispatching activities or messages.
type Result struct {
	// FailedEventIDs contains the EventIds of events whose dispatch failed.
	// Used by the pre-save path to exclude these from history so the retry
	// can regenerate and re-dispatch them.
	FailedEventIDs map[int32]struct{}
	Err            error
}

// RecordFailure records a failed dispatch for the given event ID.
func (r *Result) RecordFailure(eventID int32, err error) {
	if r.FailedEventIDs == nil {
		r.FailedEventIDs = make(map[int32]struct{})
	}
	r.FailedEventIDs[eventID] = struct{}{}
	r.Err = errors.Join(r.Err, err)
}

// HasRemoteTasks returns true if any pending task has a remote TargetAppID.
func HasRemoteTasks(es []*backend.HistoryEvent) bool {
	for _, e := range es {
		if router := e.GetRouter(); router != nil && router.TargetAppID != nil {
			return true
		}
	}
	return false
}

// HasRemoteMessages returns true if any pending message targets a remote app.
func HasRemoteMessages(msgs []*backend.OrchestrationRuntimeStateMessage) bool {
	for _, msg := range msgs {
		if router := msg.GetHistoryEvent().GetRouter(); router != nil && router.TargetAppID != nil {
			return true
		}
	}
	return false
}

// IsDispatchableEvent returns true if the event corresponds to a dispatched
// activity (TaskScheduled) or child workflow (SubOrchestrationInstanceCreated).
func IsDispatchableEvent(e *backend.HistoryEvent) bool {
	return e.GetTaskScheduled() != nil || e.GetSubOrchestrationInstanceCreated() != nil
}
