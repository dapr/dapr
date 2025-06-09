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

package workflow

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/cenkalti/backoff/v4"
	"google.golang.org/protobuf/types/known/anypb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/messages"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
)

func (w *workflow) handleInvoke(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
	w.table.RemoveIdler(w)

	if req.GetMessage() == nil {
		return nil, errors.New("message is nil in request")
	}

	// Create the InvokeMethodRequest
	imReq, err := invokev1.FromInternalInvokeRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to create InvokeMethodRequest: %w", err)
	}
	defer imReq.Close()

	policyDef := w.resiliency.ActorPostLockPolicy(w.actorType, w.actorID)
	policyRunner := resiliency.NewRunner[*internalsv1pb.InternalInvokeResponse](ctx, policyDef)
	msg := imReq.Message()
	return policyRunner(func(ctx context.Context) (*internalsv1pb.InternalInvokeResponse, error) {
		resData, err := w.executeMethod(ctx, msg.GetMethod(), msg.GetData().GetValue())
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

func (w *workflow) executeMethod(ctx context.Context, methodName string, request []byte) ([]byte, error) {
	log.Debugf("Workflow actor '%s': invoking method '%s'", w.actorID, methodName)

	if w.actorState == nil {
		return nil, messages.ErrActorRuntimeNotFound
	}

	switch methodName {
	case todo.CreateWorkflowInstanceMethod:
		return nil, w.createWorkflowInstance(ctx, request)

	case todo.AddWorkflowEventMethod:
		return nil, w.addWorkflowEvent(ctx, request)

	case todo.PurgeWorkflowStateMethod:
		return nil, w.purgeWorkflowState(ctx)

	case todo.ForkWorkflowHistory:
		return nil, backoff.Permanent(w.forkWorkflowHistory(ctx, request))

	case todo.RerunWorkflowInstance:
		return nil, backoff.Permanent(w.rerunWorkflowInstanceRequest(ctx, request))

	default:
		return nil, fmt.Errorf("no such method: %s", methodName)
	}
}

func (w *workflow) handleReminder(ctx context.Context, reminder *actorapi.Reminder) error {
	log.Debugf("Workflow actor '%s': invoking reminder '%s'", w.actorID, reminder.Name)

	completed, err := w.runWorkflow(ctx, reminder)

	if completed == todo.RunCompletedTrue {
		w.table.DeleteFromTableIn(w, time.Second*10)
	}

	// We delete the reminder on success and on non-recoverable errors.
	// Returning nil signals that we want the execution to be retried in the next period interval
	switch {
	case err == nil:
		if w.schedulerReminders {
			return nil
		}
		return actorerrors.ErrReminderCanceled
	case errors.Is(err, context.DeadlineExceeded):
		log.Warnf("Workflow actor '%s': execution timed-out and will be retried later: '%v'", w.actorID, err)
		return err
	case errors.Is(err, context.Canceled):
		log.Warnf("Workflow actor '%s': execution was canceled (process shutdown?) and will be retried later: '%v'", w.actorID, err)
		if w.schedulerReminders {
			return err
		}
		return nil
	case wferrors.IsRecoverable(err):
		log.Warnf("Workflow actor '%s': execution failed with a recoverable error and will be retried later: '%v'", w.actorID, err)
		if w.schedulerReminders {
			return err
		}
		return nil
	default: // Other error
		log.Errorf("Workflow actor '%s': execution failed with an error: %v", w.actorID, err)
		if w.schedulerReminders {
			return err
		}
		return actorerrors.ErrReminderCanceled
	}
}
