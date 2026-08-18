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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/events"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/messages"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

func (o *orchestrator) callChildWorkflows(ctx context.Context, startEventName string, es []*protos.HistoryEvent, outgoingHistory map[int32]*protos.PropagatedHistory) error {
	log.Debugf("Workflow actor '%s': calling %d child workflows", o.actorID, len(es))

	var errs []error
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
			errs = append(errs, fmt.Errorf("failed to marshal child workflow request: %w", err))
			continue
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
			if messages.IsPermissionDenied(err) {
				log.Warnf("Workflow actor '%s': child workflow denied by access policy: %v", o.actorID, err)
				if ferr := o.failChildWorkflowTask(ctx, e.GetEventId(), messages.ErrorTypeAccessPolicyDenied, messages.ErrorMessageAccessPolicyDenied); ferr != nil {
					errs = append(errs, ferr)
				}
				continue
			}
			// The target instance ID is occupied by another workflow. Fail the
			// awaited child task rather than retrying forever.
			if messages.IsAlreadyExists(err) {
				log.Warnf("Workflow actor '%s': child workflow instance ID '%s' already exists: %v", o.actorID, id, err)
				if ferr := o.failChildWorkflowTask(ctx, e.GetEventId(), messages.ErrorTypeAlreadyExists, messages.GRPCStatusMessage(err)); ferr != nil {
					errs = append(errs, ferr)
				}
				continue
			}
			errs = append(errs, fmt.Errorf("failed to call child workflow '%s': %w", id, err))
			continue
		}
	}

	return errors.Join(errs...)
}

// failChildWorkflowTask creates a ChildWorkflowInstanceFailed event on the
// parent orchestrator when the child workflow creation cannot succeed (e.g.
// rejected by a WorkflowAccessPolicy, or the target instance ID is already
// taken by another workflow). It uses a reminder-based approach to deliver the
// failure event in a fresh execution cycle, avoiding conflicts with the current
// run loop's ClearInbox/saveInternalState calls.
// taskScheduledID is the correlation ID that the parent orchestrator engine
// uses to match this failure with the original sub-orchestration request.
func (o *orchestrator) failChildWorkflowTask(ctx context.Context, taskScheduledID int32, errorType, errorMessage string) error {
	failedEvent := &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.New(time.Now()),
		Router:    &protos.TaskRouter{SourceAppID: o.appID},
		EventType: events.NewChildWorkflowFailedEventType(taskScheduledID, errorType, errorMessage, false),
	}

	// Create a reminder that carries the failure event. When this
	// reminder fires (in a fresh execution cycle after the current run
	// completes), handleReminder routes it to addWorkflowEvent which
	// adds the event to the inbox and triggers re-execution.
	reminderName, err := randomReminderName(common.ReminderPrefixActivityResult)
	if err != nil {
		return fmt.Errorf("failed to create failure reminder: %w", err)
	}
	if err := o.createWorkflowReminder(ctx, reminderName, failedEvent, time.Now(), o.appID, nil); err != nil {
		return fmt.Errorf("failed to create failure reminder: %w", err)
	}

	return nil
}
