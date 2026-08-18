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
	"github.com/dapr/durabletask-go/api/protos"
)

// NewTaskFailedEventType builds the inner oneof body for a synthetic
// TaskFailed history event whose taskScheduledId binds it to the
// original schedule event. Shared by orchestrator paths that need to
// signal an activity failure that did not originate from the activity
// itself (access policy denial, attestation verification failure).
func NewTaskFailedEventType(taskID int32, errorType, errorMessage string, nonRetriable bool) *protos.HistoryEvent_TaskFailed {
	return &protos.HistoryEvent_TaskFailed{
		TaskFailed: &protos.TaskFailedEvent{
			TaskScheduledId: taskID,
			FailureDetails: &protos.TaskFailureDetails{
				ErrorType:      errorType,
				ErrorMessage:   errorMessage,
				IsNonRetriable: nonRetriable,
			},
		},
	}
}

// NewChildWorkflowFailedEventType is the child-workflow analogue of
// NewTaskFailedEventType.
func NewChildWorkflowFailedEventType(taskID int32, errorType, errorMessage string, nonRetriable bool) *protos.HistoryEvent_ChildWorkflowInstanceFailed {
	return &protos.HistoryEvent_ChildWorkflowInstanceFailed{
		ChildWorkflowInstanceFailed: &protos.ChildWorkflowInstanceFailedEvent{
			TaskScheduledId: taskID,
			FailureDetails: &protos.TaskFailureDetails{
				ErrorType:      errorType,
				ErrorMessage:   errorMessage,
				IsNonRetriable: nonRetriable,
			},
		},
	}
}
