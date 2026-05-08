/*
Copyright 2025 The Dapr Authors
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

package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	workflowacl "github.com/dapr/dapr/pkg/acl/workflow"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/events"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

func (o *orchestrator) callChildWorkflows(ctx context.Context, startEventName string, es []*protos.HistoryEvent, outgoingHistory map[int32]*protos.PropagatedHistory) error {
	log.Debugf("Workflow actor '%s': calling %d child workflows", o.actorID, len(es))

	for _, e := range es {
		createSO := e.GetChildWorkflowInstanceCreated()

		//nolint:protogetter
		startEvent := &protos.HistoryEvent{
			EventId:   -1,
			Timestamp: timestamppb.New(time.Now()),
			Router:    e.Router,
			EventType: &protos.HistoryEvent_ExecutionStarted{
				ExecutionStarted: &protos.ExecutionStartedEvent{
					Name: createSO.Name,
					ParentInstance: &protos.ParentInstanceInfo{
						TaskScheduledId:  e.EventId,
						Name:             wrapperspb.String(startEventName),
						WorkflowInstance: &protos.WorkflowInstance{InstanceId: o.actorID},
						AppID:            new(o.appID),
					},
					Input: createSO.Input,
					WorkflowInstance: &protos.WorkflowInstance{
						InstanceId:  createSO.InstanceId,
						ExecutionId: wrapperspb.String(uuid.New().String()),
					},
					ParentTraceContext: createSO.ParentTraceContext,
				},
			},
		}

		createReq := &backend.CreateWorkflowInstanceRequest{
			StartEvent: startEvent,
		}
		if ph := outgoingHistory[e.GetEventId()]; ph != nil {
			if o.signer == nil {
				log.Warnf("Workflow actor '%s': propagating unsigned workflow history to child workflow '%s' (signing is not configured; chunks cannot be cryptographically verified by the receiver)", o.actorID, createSO.GetInstanceId())
			}
			createReq.PropagatedHistory = ph
		}

		reqP, err := proto.Marshal(createReq)
		if err != nil {
			return fmt.Errorf("failed to marshal child workflow request: %w", err)
		}

		id := e.GetChildWorkflowInstanceCreated().GetInstanceId()
		req := internalsv1pb.NewInternalInvokeRequest(todo.CreateWorkflowInstanceMethod).
			WithActor(o.actorType, id).
			WithData(reqP).
			WithContentType(invokev1.ProtobufContentType)

		_, err = o.router.Call(ctx, req)
		if err != nil {
			// If the call was denied by a workflow access policy, fail the
			// child orchestration immediately rather than retrying.
			if isPermissionDenied(err) {
				return o.failChildWorkflowACL(ctx, e.GetEventId(), err)
			}
			return fmt.Errorf("failed to call child workflow '%s': %w", id, err)
		}
	}

	return nil
}

// isPermissionDenied checks whether the error (possibly wrapped) contains a
// gRPC PermissionDenied status code. Walks both single-error and multi-error
// chains.
func isPermissionDenied(err error) bool {
	if err == nil {
		return false
	}

	// Try direct gRPC status extraction.
	if st, ok := status.FromError(err); ok && st.Code() == codes.PermissionDenied {
		return true
	}

	// Walk the full error chain. errors.As traverses both Unwrap() error
	// and Unwrap() []error chains (multi-error wrappers like errors.Join).
	var wrapped interface{ GRPCStatus() *status.Status }
	if errors.As(err, &wrapped) {
		if wrapped.GRPCStatus().Code() == codes.PermissionDenied {
			return true
		}
	}

	return false
}

// isPermissionDeniedRequiresUnmet returns true when the error is a
// PermissionDenied carrying the requires-unmet sentinel suffix. The marker
// constant lives in pkg/acl/workflow so the producer (gRPC handler / per-actor
// enforcement) and consumer (orchestrator deny-translation) stay in sync.
func isPermissionDeniedRequiresUnmet(err error) bool {
	if err == nil {
		return false
	}
	if st, ok := status.FromError(err); ok && st.Code() == codes.PermissionDenied {
		return strings.Contains(st.Message(), workflowacl.DeniedMarkerRequiresUnmet)
	}
	var wrapped interface{ GRPCStatus() *status.Status }
	if errors.As(err, &wrapped) {
		s := wrapped.GRPCStatus()
		if s.Code() == codes.PermissionDenied {
			return strings.Contains(s.Message(), workflowacl.DeniedMarkerRequiresUnmet)
		}
	}
	return false
}

// aclFailureType returns the (ErrorType, ErrorMessage) pair to write on a
// TaskFailed/ChildWorkflowInstanceFailed event for a policy denial.
// Public messages stay opaque about which rule/entry was violated; the type
// lets workflow authors branch on cause programmatically.
func aclFailureType(err error) (string, string) {
	if isPermissionDeniedRequiresUnmet(err) {
		return "WorkflowAccessPolicyRequiresUnmet",
			"access denied by workflow access policy: required history not satisfied"
	}
	return "WorkflowAccessPolicyDenied",
		"access denied by workflow access policy"
}

// failChildWorkflowACL creates a ChildWorkflowInstanceFailed event on the
// parent orchestrator when the child workflow call is rejected by a
// WorkflowAccessPolicy. It uses a reminder-based approach to deliver the
// failure event in a fresh execution cycle, avoiding conflicts with the current
// run loop's ClearInbox/saveInternalState calls.
// taskScheduledID is the correlation ID that the parent orchestrator engine
// uses to match this failure with the original sub-orchestration request.
func (o *orchestrator) failChildWorkflowACL(ctx context.Context, taskScheduledID int32, callErr error) error {
	errType, errMsg := aclFailureType(callErr)
	failedEvent := &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.New(time.Now()),
		EventType: events.NewChildWorkflowFailedEventType(taskScheduledID, errType, errMsg, false),
	}

	log.Warnf("Workflow actor '%s': child workflow denied by access policy: %v", o.actorID, callErr)

	// Create a reminder that carries the failure event. When this
	// reminder fires (in a fresh execution cycle after the current run
	// completes), handleReminder routes it to addWorkflowEvent which
	// adds the event to the inbox and triggers re-execution.
	if _, err := o.createWorkflowReminder(ctx, common.ReminderPrefixActivityResult, failedEvent, time.Now(), o.appID, nil); err != nil {
		return fmt.Errorf("failed to create failure reminder: %w", err)
	}

	return nil
}
