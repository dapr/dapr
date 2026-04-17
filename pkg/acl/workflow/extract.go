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

	"github.com/dapr/durabletask-go/backend"
)

const (
	actorTypePrefix = "dapr.internal."
	suffixWorkflow  = ".workflow"
	suffixActivity  = ".activity"

	methodCreateWorkflowInstance = "CreateWorkflowInstance"
	methodExecute                = "Execute"
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
	op, _, _, ok := SplitActorType(actorType)
	return op, ok
}

// SplitActorType extracts the (operation, namespace, appID) triple from a
// workflow/activity actor type string of the form
// "dapr.internal.<namespace>.<appID>.<workflow|activity>". Returns ok=false
// if the input doesn't match that shape. AppIDs may not contain dots; the
// renderer always emits "<ns>.<app>.<kind>" so the last dot-separated
// segment before the suffix is the appID and everything before it is the
// namespace (which may itself contain dots).
func SplitActorType(actorType string) (op OperationType, namespace, appID string, ok bool) {
	if !strings.HasPrefix(actorType, actorTypePrefix) {
		return "", "", "", false
	}
	rest := actorType[len(actorTypePrefix):]
	switch {
	case strings.HasSuffix(rest, suffixWorkflow):
		op = OperationTypeWorkflow
		rest = strings.TrimSuffix(rest, suffixWorkflow)
	case strings.HasSuffix(rest, suffixActivity):
		op = OperationTypeActivity
		rest = strings.TrimSuffix(rest, suffixActivity)
	default:
		return "", "", "", false
	}
	dot := strings.LastIndexByte(rest, '.')
	if dot <= 0 || dot == len(rest)-1 {
		return op, "", "", true
	}
	return op, rest[:dot], rest[dot+1:], true
}

// ExtractOperationName extracts the workflow or activity name from the request
// method and payload. Returns the name and true if extraction succeeded, or
// empty string and false if the method is not subject to access control
// (e.g. AddWorkflowEvent, PurgeWorkflowState).
func ExtractOperationName(opType OperationType, method string, data []byte) (string, bool, error) {
	switch opType {
	case OperationTypeWorkflow:
		return extractWorkflowName(method, data)
	case OperationTypeActivity:
		return extractActivityName(method, data)
	default:
		return "", false, nil
	}
}

func extractWorkflowName(method string, data []byte) (string, bool, error) {
	if method != methodCreateWorkflowInstance {
		// Only CreateWorkflowInstance is subject to access control (schedule
		// operation).
		return "", false, nil
	}

	var req backend.CreateWorkflowInstanceRequest
	if err := proto.Unmarshal(data, &req); err != nil {
		return "", false, fmt.Errorf("failed to unmarshal CreateWorkflowInstanceRequest: %w", err)
	}

	es := req.GetStartEvent().GetExecutionStarted()
	if es == nil {
		return "", false, errors.New("CreateWorkflowInstanceRequest missing ExecutionStarted event")
	}

	return es.GetName(), true, nil
}

func extractActivityName(method string, data []byte) (string, bool, error) {
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
