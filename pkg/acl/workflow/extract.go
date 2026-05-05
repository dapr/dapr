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

	"github.com/dapr/durabletask-go/api/protos"
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

// ExtractRequest extracts the workflow or activity name AND the optional
// PropagatedHistory carried by the request payload. The PropagatedHistory is
// used by RequiredEvent rules to gate operations on the caller's prior
// history.
func ExtractRequest(opType OperationType, method string, data []byte) (string, *protos.PropagatedHistory, bool, error) {
	switch opType {
	case OperationTypeWorkflow:
		return extractWorkflowRequest(method, data)
	case OperationTypeActivity:
		return extractActivityRequest(method, data)
	default:
		return "", nil, false, nil
	}
}

func extractWorkflowRequest(method string, data []byte) (string, *protos.PropagatedHistory, bool, error) {
	if method != methodCreateWorkflowInstance {
		// Only CreateWorkflowInstance is subject to access control (schedule
		// operation).
		return "", nil, false, nil
	}

	var req backend.CreateWorkflowInstanceRequest
	if err := proto.Unmarshal(data, &req); err != nil {
		return "", nil, false, fmt.Errorf("failed to unmarshal CreateWorkflowInstanceRequest: %w", err)
	}

	es := req.GetStartEvent().GetExecutionStarted()
	if es == nil {
		return "", nil, false, errors.New("CreateWorkflowInstanceRequest missing ExecutionStarted event")
	}

	return es.GetName(), req.GetPropagatedHistory(), true, nil
}

func extractActivityRequest(method string, data []byte) (string, *protos.PropagatedHistory, bool, error) {
	if method != methodExecute {
		return "", nil, false, nil
	}

	// HistoryEvent & optional PropagatedHistory. Try the ActivityInvocation first
	// and fall back to the legacy raw HistoryEvent payload (which has no
	// propagated history).
	var invocation protos.ActivityInvocation
	if envErr := proto.Unmarshal(data, &invocation); envErr == nil && invocation.GetHistoryEvent() != nil {
		ts := invocation.GetHistoryEvent().GetTaskScheduled()
		if ts == nil {
			return "", nil, false, errors.New("activity HistoryEvent missing TaskScheduled")
		}
		return ts.GetName(), invocation.GetPropagatedHistory(), true, nil
	}

	var his backend.HistoryEvent
	if err := proto.Unmarshal(data, &his); err != nil {
		return "", nil, false, fmt.Errorf("failed to unmarshal activity HistoryEvent: %w", err)
	}

	ts := his.GetTaskScheduled()
	if ts == nil {
		return "", nil, false, errors.New("activity HistoryEvent missing TaskScheduled")
	}

	return ts.GetName(), nil, true, nil
}
