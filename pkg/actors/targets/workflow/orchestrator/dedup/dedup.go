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

package dedup

import (
	"github.com/dapr/durabletask-go/backend"
	dtdedup "github.com/dapr/durabletask-go/backend/runtimestate/dedup"
)

// IsDuplicateCompletion reports whether e is a TaskCompleted / TaskFailed /
// TimerFired / ChildWorkflowInstance{Completed,Failed} that already has a
// matching resolution recorded in either history or inbox. Returns false for
// events without resolution semantics (e.g. EventRaised, ExecutionTerminated):
// those are user/control-plane events that may legitimately repeat.
func IsDuplicateCompletion(e *backend.HistoryEvent, history, inbox []*backend.HistoryEvent) bool {
	kind, id, ok := dtdedup.Of(e)
	if !ok {
		return false
	}
	return dtdedup.IsPresent(history, kind, id) || dtdedup.IsPresent(inbox, kind, id)
}

// IsTaskAlreadyResolved reports whether a TaskScheduled event has a matching
// TaskCompleted or TaskFailed resolution already in history or queued in the
// inbox.
func IsTaskAlreadyResolved(taskScheduled *backend.HistoryEvent, history, inbox []*backend.HistoryEvent) bool {
	if taskScheduled.GetTaskScheduled() == nil {
		return false
	}
	id := taskScheduled.GetEventId()
	return dtdedup.IsPresent(history, dtdedup.KindTask, id) ||
		dtdedup.IsPresent(inbox, dtdedup.KindTask, id)
}
