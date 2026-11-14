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

package state

import (
	"github.com/dapr/durabletask-go/api/protos"
)

// StartOrdering records the place a keyed instance takes in its ordering
// key's queue when it is created with start.
func (s *State) StartOrdering(start *protos.ExecutionStartedEvent) {
	if start.GetOrdering() == nil {
		return
	}
	s.Ordering = &protos.BackendWorkflowOrderingMetadata{
		CreateExecutionId: start.GetWorkflowInstance().GetExecutionId().GetValue(),
	}
}

// ExecutionStarted is the instance's start event: from history, or from the
// inbox before the first turn commits it. Nil when the instance has none.
func (s *State) ExecutionStarted() *protos.ExecutionStartedEvent {
	return executionStartedOf(s.History, s.Inbox)
}

// OrderingOptions is the ordering the instance was started with, nil for an
// unkeyed instance.
func (s *State) OrderingOptions() *protos.WorkflowOrdering {
	if s == nil || s.Ordering == nil {
		return nil
	}
	return s.ExecutionStarted().GetOrdering()
}

// OrderingGated reports whether the instance waits for its ordering key to
// let it run: it is keyed, not released, and has not run a turn.
func (s *State) OrderingGated() bool {
	return s.Ordering != nil && !s.Ordering.GetReleased() && len(s.History) == 0 &&
		executionStartedOf(nil, s.Inbox) != nil
}

// OrderingDoneOwed reports whether the instance completed without its
// ordering key acknowledging that it no longer holds it. It is derived from
// the state rather than set by the completing turn, so that every way an
// instance completes owes it.
func (s *State) OrderingDoneOwed() bool {
	return s.Ordering != nil && !s.Ordering.GetDone() && s.IsCompleted()
}
