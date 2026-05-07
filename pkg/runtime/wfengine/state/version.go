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
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/durabletask-go/backend"
)

// WorkflowVersion returns the version recorded on the most recent
// WorkflowStarted history event, or nil if none is set.
//
// The runtime-resolved version is *not* stored on ExecutionStartedEvent
// (the event returned by WorkflowRuntimeState.GetStartEvent), so reading
// rstate.GetStartEvent().GetVersion() returns nil.
func WorkflowVersion(events []*backend.HistoryEvent) *wrapperspb.StringValue {
	for i := len(events) - 1; i >= 0; i-- {
		ws := events[i].GetWorkflowStarted()
		if ws == nil {
			continue
		}
		if name := ws.GetVersion().GetName(); name != "" {
			return wrapperspb.String(name)
		}
	}
	return nil
}
