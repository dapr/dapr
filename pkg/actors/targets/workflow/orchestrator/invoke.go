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
	"net/http"
	"strings"

	"github.com/cenkalti/backoff/v4"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/dapr/pkg/messages"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/backend"
)

func (o *orchestrator) handleInvoke(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
	if req.GetMessage() == nil {
		return nil, errors.New("message is nil in request")
	}

	method := req.GetMessage().GetMethod()
	data := req.GetMessage().GetData().GetValue()

	// AddWorkflowEvent encodes the operation in its HistoryEvent payload, and
	// the orchestrator processes the same parsed event for its own logic.
	// Unmarshal once here, share with both the access policy check and
	// addWorkflowEvent.
	var parsedAddEvent *backend.HistoryEvent
	if method == todo.AddWorkflowEventMethod {
		var ev backend.HistoryEvent
		if err := proto.Unmarshal(data, &ev); err != nil {
			return nil, fmt.Errorf("failed to unmarshal AddWorkflowEvent HistoryEvent: %w", err)
		}
		parsedAddEvent = &ev
	}

	if err := o.checkAccessPolicy(ctx, method, data, parsedAddEvent, req.GetMetadata()); err != nil {
		return nil, err
	}

	// Create the InvokeMethodRequest
	imReq, err := invokev1.FromInternalInvokeRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to create InvokeMethodRequest: %w", err)
	}
	defer imReq.Close()

	policyDef := o.resiliency.ActorPostLockPolicy(o.actorType, o.actorID)
	policyRunner := resiliency.NewRunner[*internalsv1pb.InternalInvokeResponse](ctx, policyDef)
	msg := imReq.Message()
	return policyRunner(func(ctx context.Context) (*internalsv1pb.InternalInvokeResponse, error) {
		resData, err := o.executeMethod(ctx, msg.GetMethod(), req.GetMetadata(), msg.GetData().GetValue(), parsedAddEvent)
		if err != nil {
			return nil, err
		}

		return &internalsv1pb.InternalInvokeResponse{
			Status: &internalsv1pb.Status{
				Code: http.StatusOK,
			},
			Message: &commonv1pb.InvokeResponse{
				Data: &anypb.Any{
					Value: resData,
				},
			},
		}, nil
	})
}

func (o *orchestrator) executeMethod(ctx context.Context, methodName string, meta map[string]*internalsv1pb.ListStringValue, request []byte, parsedAddEvent *backend.HistoryEvent) ([]byte, error) {
	log.Debugf("Workflow actor '%s': invoking method '%s'", o.actorID, methodName)

	if o.actorState == nil {
		return nil, messages.ErrActorRuntimeNotFound
	}

	switch methodName {
	case todo.CreateWorkflowInstanceMethod:
		return nil, o.createWorkflowInstance(ctx, request)

	case todo.AddWorkflowEventMethod:
		return nil, o.addWorkflowEvent(ctx, parsedAddEvent)

	case todo.PurgeWorkflowStateMethod:
		return nil, o.purgeWorkflowState(ctx, meta)

	case todo.RecursivePurgeWorkflowStateMethod:
		return o.recursivePurgeWorkflowState(ctx, meta)

	case todo.ForkWorkflowHistory:
		return nil, backoff.Permanent(o.forkWorkflowHistory(ctx, request))

	case todo.RerunWorkflowInstance:
		return nil, backoff.Permanent(o.rerunWorkflowInstanceRequest(ctx, request))

	default:
		return nil, fmt.Errorf("no such method: %s", methodName)
	}
}

func (o *orchestrator) handleReminder(ctx context.Context, reminder *actorapi.Reminder) error {
	log.Debugf("Workflow actor '%s': invoking reminder '%s'", o.actorID, reminder.Name)

	switch {
	case strings.HasPrefix(reminder.Name, reminderPrefixStart),
		strings.HasPrefix(reminder.Name, reminderPrefixNewEvent),
		strings.HasPrefix(reminder.Name, reminderPrefixTimer):
		return o.runWorkflowFromReminder(ctx, reminder)

	case strings.HasPrefix(reminder.Name, common.ReminderPrefixActivityResult):
		var ev backend.HistoryEvent
		if err := proto.Unmarshal(reminder.Data.GetValue(), &ev); err != nil {
			return fmt.Errorf("failed to unmarshal activity-result HistoryEvent: %w", err)
		}
		return o.addWorkflowEvent(ctx, &ev)

	default:
		return fmt.Errorf("unable to handle reminder '%s' for workflow actor '%s': unknown reminder type", reminder.Name, o.actorID)
	}
}

func (o *orchestrator) runWorkflowFromReminder(ctx context.Context, reminder *actorapi.Reminder) error {
	completed, err := o.runWorkflow(ctx, reminder)
	if completed == todo.RunCompletedTrue {
		defer o.deactivate(o)
	}

	// We delete the reminder on success and on non-recoverable errors.
	// Returning nil signals that we want the execution to be retried in the next period interval
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.DeadlineExceeded):
		log.Warnf("Workflow actor '%s': execution timed-out and will be retried later: '%v'", o.actorID, err)
		return err
	case errors.Is(err, context.Canceled):
		log.Warnf("Workflow actor '%s': execution was canceled (process shutdown?) and will be retried later: '%v'", o.actorID, err)
		return err
	case wferrors.IsRecoverable(err):
		log.Warnf("Workflow actor '%s': execution failed with a recoverable error and will be retried later: '%v'", o.actorID, err)
		return err
	default: // Other error
		log.Errorf("Workflow actor '%s': execution failed with an error: %v", o.actorID, err)
		return err
	}
}
