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
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// TerminationInput returns the input attached to the first termination event
// in history order. Dapr uses this existing field to carry a termination
// reason without extending the Durable Task protocol.
func TerminationInput(state *backend.WorkflowRuntimeState) *wrapperspb.StringValue {
	if state == nil {
		return nil
	}

	for _, events := range [][]*backend.HistoryEvent{state.GetOldEvents(), state.GetNewEvents()} {
		for _, event := range events {
			if terminated := event.GetExecutionTerminated(); terminated != nil {
				return terminated.GetInput()
			}
		}
	}

	return nil
}

// CustomStatus returns the termination input when the workflow was
// terminated with a reason; otherwise it returns the status set by the
// workflow implementation.
func CustomStatus(state *backend.WorkflowRuntimeState) *wrapperspb.StringValue {
	if state == nil {
		return nil
	}

	if runtimestate.RuntimeStatus(state) == protos.OrchestrationStatus_ORCHESTRATION_STATUS_TERMINATED {
		if input := TerminationInput(state); input != nil {
			return input
		}
	}
	return state.GetCustomStatus()
}
