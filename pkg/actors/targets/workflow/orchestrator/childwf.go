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
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

func (o *orchestrator) callChildWorkflows(ctx context.Context, startEventName string, es []*protos.HistoryEvent) error {
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

		reqP, err := proto.Marshal(&backend.CreateWorkflowInstanceRequest{
			StartEvent: startEvent,
		})
		if err != nil {
			return fmt.Errorf("failed to marshal child workflow request: %w", err)
		}

		id := e.GetChildWorkflowInstanceCreated().GetInstanceId()

		switch o.classifyRouting(e.GetRouter()) {
		case RoutingCrossNS:
			targetNs := e.GetRouter().GetTargetNamespace()
			targetAppID := e.GetRouter().GetTargetAppID()
			targetActorType := o.actorTypeBuilder.WorkflowNS(targetNs, targetAppID)
			parentExecID := ""
			if rs := o.rstate; rs != nil {
				parentExecID = rs.GetStartEvent().GetWorkflowInstance().GetExecutionId().GetValue()
			}
			childExecID := startEvent.GetExecutionStarted().GetWorkflowInstance().GetExecutionId().GetValue()
			if xerr := o.dispatchCrossNS(ctx,
				targetNs, targetAppID,
				targetActorType, id,
				todo.CreateWorkflowInstanceMethod,
				reqP,
				parentExecID, id, childExecID,
				e.GetEventId(),
			); xerr != nil {
				return fmt.Errorf("failed to dispatch cross-namespace child workflow '%s' to '%s/%s': %w", id, targetNs, targetAppID, xerr)
			}
			continue

		case RoutingLocal, RoutingCrossApp:
			req := internalsv1pb.NewInternalInvokeRequest(todo.CreateWorkflowInstanceMethod).
				WithActor(o.actorType, id).
				WithData(reqP).
				WithContentType(invokev1.ProtobufContentType)

			if _, err = o.router.Call(ctx, req); err != nil {
				if isPermissionDenied(err) {
					return o.failChildWorkflow(ctx, e.GetEventId(), err)
				}
				return fmt.Errorf("failed to call child workflow '%s': %w", id, err)
			}
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

// failChildWorkflow creates a ChildWorkflowInstanceFailed event on the parent
// orchestrator when the child workflow call is permanently rejected (e.g. by a
// WorkflowAccessPolicy). It uses a reminder-based approach to deliver the
// failure event in a fresh execution cycle, avoiding conflicts with the current
// run loop's ClearInbox/saveInternalState calls.
// taskScheduledID is the correlation ID that the parent orchestrator engine
// uses to match this failure with the original sub-orchestration request.
func (o *orchestrator) failChildWorkflow(ctx context.Context, taskScheduledID int32, callErr error) error {
	failedEvent := &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.New(time.Now()),
		EventType: &protos.HistoryEvent_ChildWorkflowInstanceFailed{
			ChildWorkflowInstanceFailed: &protos.ChildWorkflowInstanceFailedEvent{
				TaskScheduledId: taskScheduledID,
				FailureDetails: &protos.TaskFailureDetails{
					ErrorType:    "WorkflowAccessPolicyDenied",
					ErrorMessage: "operation denied by workflow access policy",
				},
			},
		},
	}

	log.Warnf("Workflow actor '%s': child workflow denied by access policy: %v", o.actorID, callErr)

	// Create a reminder that carries the failure event. When this
	// reminder fires (in a fresh execution cycle after the current run
	// completes), handleReminder routes it to addWorkflowEvent which
	// adds the event to the inbox and triggers re-execution.
	if _, err := o.createWorkflowReminder(ctx, common.ReminderPrefixActivityResult, failedEvent, time.Now(), o.appID); err != nil {
		return fmt.Errorf("failed to create failure reminder: %w", err)
	}

	return nil
}
