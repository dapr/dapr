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

package payloadstore

import (
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/durabletask-go/api/protos"
)

// Payload returns the user-payload field of e, or nil when the event type
// carries no user payload or the field is unset. It is the single
// enumeration of payload-bearing history event types: the offload pass
// and any dereference hook must both go through it so they can never
// disagree on which fields hold payloads. Callers read and mutate the
// returned StringValue's Value in place.
//
// Deliberately excluded: TaskFailureDetails (error message and stack
// trace on failure events) is structured diagnostic data, not a user
// payload, and version/span-ID StringValue fields are small system
// metadata. TestPayloadAccessorCompleteness enforces that every other
// StringValue field on a HistoryEvent variant is covered here.
func Payload(e *protos.HistoryEvent) *wrapperspb.StringValue {
	switch typed := e.GetEventType().(type) {
	case *protos.HistoryEvent_ExecutionStarted:
		return typed.ExecutionStarted.GetInput()
	case *protos.HistoryEvent_ExecutionCompleted:
		return typed.ExecutionCompleted.GetResult()
	case *protos.HistoryEvent_ExecutionTerminated:
		return typed.ExecutionTerminated.GetInput()
	case *protos.HistoryEvent_TaskScheduled:
		return typed.TaskScheduled.GetInput()
	case *protos.HistoryEvent_TaskCompleted:
		return typed.TaskCompleted.GetResult()
	case *protos.HistoryEvent_ChildWorkflowInstanceCreated:
		return typed.ChildWorkflowInstanceCreated.GetInput()
	case *protos.HistoryEvent_ChildWorkflowInstanceCompleted:
		return typed.ChildWorkflowInstanceCompleted.GetResult()
	case *protos.HistoryEvent_EventSent:
		return typed.EventSent.GetInput()
	case *protos.HistoryEvent_EventRaised:
		return typed.EventRaised.GetInput()
	case *protos.HistoryEvent_ContinueAsNew:
		return typed.ContinueAsNew.GetInput()
	case *protos.HistoryEvent_ExecutionSuspended:
		return typed.ExecutionSuspended.GetInput()
	case *protos.HistoryEvent_ExecutionResumed:
		return typed.ExecutionResumed.GetInput()
	default:
		return nil
	}
}
