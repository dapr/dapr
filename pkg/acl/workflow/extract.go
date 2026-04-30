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

package workflow

import (
	"errors"
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"

	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/backend"
)

const (
	actorTypePrefix = "dapr.internal."
	suffixWorkflow  = ".workflow"
	suffixActivity  = ".activity"

	methodExecute = "Execute"
)

// OperationType represents the type of workflow operation being performed.
type OperationType string

const (
	OperationTypeWorkflow OperationType = "workflow"
	OperationTypeActivity OperationType = "activity"
)

// ParseActorType determines if an actor type represents a workflow or activity
// actor. Returns the operation type and true if it is a workflow/activity actor,
// or empty string and false otherwise.
func ParseActorType(actorType string) (OperationType, bool) {
	if !strings.HasPrefix(actorType, actorTypePrefix) {
		return "", false
	}

	switch {
	case strings.HasSuffix(actorType, suffixWorkflow):
		return OperationTypeWorkflow, true
	case strings.HasSuffix(actorType, suffixActivity):
		return OperationTypeActivity, true
	default:
		return "", false
	}
}

// AddWorkflowEvent's operation is encoded in the HistoryEvent payload;
// parsedAddEvent must be non-nil for that method so we don't unmarshal
// twice on the hot path. nil for other methods.
func WorkflowOperationFromMethod(method string, parsedAddEvent *backend.HistoryEvent) (wfaclapi.WorkflowOperation, bool, error) {
	switch method {
	case todo.CreateWorkflowInstanceMethod:
		return wfaclapi.WorkflowOperationSchedule, true, nil

	case todo.AddWorkflowEventMethod:
		if parsedAddEvent == nil {
			return "", true, errors.New("AddWorkflowEvent: parsed event is required to derive the operation")
		}
		op, err := operationFromHistoryEvent(parsedAddEvent)
		return op, true, err

	case todo.PurgeWorkflowStateMethod:
		return wfaclapi.WorkflowOperationPurge, true, nil

	case todo.WaitForRuntimeStatus:
		return wfaclapi.WorkflowOperationGet, true, nil

	case todo.ForkWorkflowHistory, todo.RerunWorkflowInstance:
		return wfaclapi.WorkflowOperationRerun, true, nil

	default:
		return "", false, nil
	}
}

func ActivityNameFromExecute(method string, data []byte) (string, bool, error) {
	if method != methodExecute {
		return "", false, nil
	}

	var his backend.HistoryEvent
	if err := proto.Unmarshal(data, &his); err != nil {
		return "", false, fmt.Errorf("failed to unmarshal activity HistoryEvent: %w", err)
	}
	ts := his.GetTaskScheduled()
	if ts == nil {
		return "", false, errors.New("activity HistoryEvent missing TaskScheduled")
	}
	return ts.GetName(), true, nil
}

func WorkflowNameFromCreateRequest(data []byte) (string, error) {
	var req backend.CreateWorkflowInstanceRequest
	if err := proto.Unmarshal(data, &req); err != nil {
		return "", fmt.Errorf("failed to unmarshal CreateWorkflowInstanceRequest: %w", err)
	}
	es := req.GetStartEvent().GetExecutionStarted()
	if es == nil {
		return "", errors.New("CreateWorkflowInstanceRequest missing ExecutionStarted event")
	}
	return es.GetName(), nil
}

func operationFromHistoryEvent(ev *backend.HistoryEvent) (wfaclapi.WorkflowOperation, error) {
	switch {
	case ev.GetExecutionTerminated() != nil:
		return wfaclapi.WorkflowOperationTerminate, nil
	case ev.GetEventRaised() != nil:
		return wfaclapi.WorkflowOperationRaise, nil
	case ev.GetExecutionSuspended() != nil:
		return wfaclapi.WorkflowOperationPause, nil
	case ev.GetExecutionResumed() != nil:
		return wfaclapi.WorkflowOperationResume, nil
	default:
		return "", fmt.Errorf("AddWorkflowEvent HistoryEvent has unsupported event type %T", ev.GetEventType())
	}
}
