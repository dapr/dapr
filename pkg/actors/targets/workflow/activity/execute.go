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

package activity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	diag "github.com/dapr/dapr/pkg/diagnostics"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
)

func (a *activity) executeActivity(ctx context.Context, name string, taskEvent *backend.HistoryEvent) (todo.RunCompleted, error) {
	activityName := ""
	if ts := taskEvent.GetTaskScheduled(); ts != nil {
		activityName = ts.GetName()
	} else {
		return todo.RunCompletedTrue, fmt.Errorf("invalid activity task event: '%s'", taskEvent.String())
	}

	endIndex := strings.Index(a.actorID, "::")
	if endIndex < 0 {
		return todo.RunCompletedTrue, fmt.Errorf("invalid activity actor ID: '%s'", a.actorID)
	}
	workflowID := a.actorID[0:endIndex]

	wi := &backend.ActivityWorkItem{
		SequenceNumber: int64(taskEvent.GetEventId()),
		InstanceID:     api.InstanceID(workflowID),
		NewEvent:       taskEvent,
		Properties:     make(map[string]any),
	}

	// Executing activity code is a one-way operation. We must wait for the app code to report its completion, which
	// will trigger this callback channel.
	// TODO: Need to come up with a design for timeouts. Some activities may need to run for hours but we also need
	//       to handle the case where the app crashes and never responds to the workflow. It may be necessary to
	//       introduce some kind of heartbeat protocol to help identify such cases.
	callback := make(chan bool, 1)
	wi.Properties[todo.CallbackChannelProperty] = callback
	log.Debugf("Activity actor '%s': scheduling activity '%s' for workflow with instanceId '%s'", a.actorID, name, wi.InstanceID)
	elapsed := float64(0)
	start := time.Now()
	err := a.scheduler(ctx, wi)
	elapsed = diag.ElapsedSince(start)

	if errors.Is(err, context.DeadlineExceeded) {
		diag.DefaultWorkflowMonitoring.ActivityOperationEvent(ctx, activityName, diag.StatusRecoverable, elapsed)
		return todo.RunCompletedFalse, wferrors.NewRecoverable(fmt.Errorf("timed-out trying to schedule an activity execution - this can happen if too many activities are running in parallel or if the workflow engine isn't running: %w", err))
	} else if err != nil {
		diag.DefaultWorkflowMonitoring.ActivityOperationEvent(ctx, activityName, diag.StatusRecoverable, elapsed)
		return todo.RunCompletedFalse, wferrors.NewRecoverable(fmt.Errorf("failed to schedule an activity execution: %w", err))
	}
	diag.DefaultWorkflowMonitoring.ActivityOperationEvent(ctx, activityName, diag.StatusSuccess, elapsed)

	// Activity execution started
	start = time.Now()
	executionStatus := ""
	elapsed = float64(0)
	// Record metrics on exit
	defer func() {
		if executionStatus != "" {
			diag.DefaultWorkflowMonitoring.ActivityExecutionEvent(ctx, activityName, executionStatus, elapsed)
		}
	}()

	select {
	case <-ctx.Done():
		// Activity execution failed with recoverable error
		elapsed = diag.ElapsedSince(start)
		executionStatus = diag.StatusRecoverable
		return todo.RunCompletedFalse, ctx.Err() // will be retried
	case completed := <-callback:
		elapsed = diag.ElapsedSince(start)
		if !completed {
			// Activity execution failed with recoverable error
			executionStatus = diag.StatusRecoverable
			return todo.RunCompletedFalse, wferrors.NewRecoverable(todo.ErrExecutionAborted) // AbandonActivityWorkItem was called
		}
	}
	log.Debugf("Activity actor '%s': activity completed for workflow with instanceId '%s' activityName '%s'", a.actorID, wi.InstanceID, name)

	// publish the result back to the workflow actor as a new event to be processed
	resultData, err := proto.Marshal(wi.Result)
	if err != nil {
		// Returning non-recoverable error
		executionStatus = diag.StatusFailed
		return todo.RunCompletedTrue, err
	}

	req := internalsv1pb.
		NewInternalInvokeRequest(todo.AddWorkflowEventMethod).
		WithActor(a.workflowActorType, workflowID).
		WithData(resultData).
		WithContentType(invokev1.ProtobufContentType)

	_, err = a.router.Call(ctx, req)
	switch {
	case err != nil:
		// Returning recoverable error, record metrics
		executionStatus = diag.StatusRecoverable
		return todo.RunCompletedFalse, wferrors.NewRecoverable(fmt.Errorf("failed to invoke '%s' method on workflow actor: %w", todo.AddWorkflowEventMethod, err))
	case wi.Result.GetTaskCompleted() != nil:
		// Activity execution completed successfully
		executionStatus = diag.StatusSuccess
	case wi.Result.GetTaskFailed() != nil:
		// Activity execution failed
		executionStatus = diag.StatusFailed
	}
	return todo.RunCompletedTrue, nil
}
