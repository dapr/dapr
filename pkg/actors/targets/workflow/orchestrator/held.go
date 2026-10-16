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

package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	staterrors "github.com/dapr/dapr/pkg/runtime/wfengine/state/errors"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

// Replay of held operations (the WorkflowsHeldOperations preview): a start or
// a new event that a daprd of this app held as a reminder while no daprd
// hosted this workflow actor type (backends/actors/held.go). Returning an
// error retries the replay under the reminder's bounded failure policy;
// returning nil acks it.

// replayHeldStart replays a held workflow start.
func (o *orchestrator) replayHeldStart(ctx context.Context, reminder *actorapi.Reminder) error {
	if !o.heldByThisApp(reminder) {
		return nil
	}

	var req backend.CreateWorkflowInstanceRequest
	if err := reminder.Data.UnmarshalTo(&req); err != nil {
		log.Warnf("Workflow actor '%s': dropping held start that cannot be decoded: %v", o.actorID, err)
		return nil
	}

	startEvent := req.GetStartEvent()
	if common.HeldOperationExpired(startEvent.GetTimestamp().AsTime()) {
		return o.failExpiredHeldStart(ctx, startEvent)
	}

	err := o.createWorkflowInstance(ctx, reminder.Data.GetValue())
	if status.Code(err) == codes.AlreadyExists {
		// The caller was told the start was accepted, so this is the only
		// place the refusal can be reported.
		log.Warnf("Workflow actor '%s': dropping held start: %v", o.actorID, err)
		return nil
	}
	return err
}

// replayHeldEvent replays a held raise event, terminate, suspend or resume.
func (o *orchestrator) replayHeldEvent(ctx context.Context, reminder *actorapi.Reminder) error {
	if !o.heldByThisApp(reminder) {
		return nil
	}

	var e backend.HistoryEvent
	if err := reminder.Data.UnmarshalTo(&e); err != nil {
		log.Warnf("Workflow actor '%s': dropping held event that cannot be decoded: %v", o.actorID, err)
		return nil
	}

	if common.HeldOperationExpired(e.GetTimestamp().AsTime()) {
		log.Warnf("Workflow actor '%s': dropping %T event held for longer than %s while no workflow worker was connected", o.actorID, e.GetEventType(), common.HeldOperationTimeout())
		diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.AddEvent, diag.StatusHeldExpired, 0)
		return nil
	}

	err := o.addWorkflowEvent(ctx, &e, completionSender{})
	if errors.Is(err, api.ErrInstanceNotFound) {
		// Unlike the other reminder arms, which ack here because a purged
		// instance looks the same, retry: the held start of this instance
		// may not have replayed yet.
		log.Debugf("Workflow actor '%s': held %T event found no instance yet, retrying", o.actorID, e.GetEventType())
	}
	return err
}

// heldByThisApp reports whether a held operation was held by this app. The
// scheduler only lets this app create these reminders on its workflow actor
// type, so anything else is dropped.
func (o *orchestrator) heldByThisApp(reminder *actorapi.Reminder) bool {
	if reminder.SourceAppID == "" || reminder.SourceAppID == o.appID {
		return true
	}
	log.Warnf("Workflow actor '%s': dropping held operation reminder '%s' from app '%s'", o.actorID, reminder.Name, reminder.SourceAppID)
	return false
}

// failExpiredHeldStart records a start held past the hold timeout as a
// FAILED instance, so a client polling the instance it was told had started
// learns what happened to it.
func (o *orchestrator) failExpiredHeldStart(ctx context.Context, startEvent *backend.HistoryEvent) error {
	diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.CreateWorkflow, diag.StatusHeldExpired, 0)

	state, _, err := o.loadInternalState(ctx)
	if err != nil {
		return err
	}
	if state != nil {
		// The instance ID was taken while this start was held: there is no
		// instance to record the failure on.
		log.Warnf("Workflow actor '%s': dropping start held for longer than %s while no workflow worker was connected", o.actorID, common.HeldOperationTimeout())
		return nil
	}

	msg := fmt.Sprintf("the workflow start was held for longer than %s while no workflow worker was connected", common.HeldOperationTimeout())
	log.Warnf("Workflow actor '%s': %s; failing the workflow instance", o.actorID, msg)

	now := time.Now()
	state = wfenginestate.NewState(o.stateOptions())
	state.AddToHistory(startEvent)
	state.AddToHistory(&backend.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.New(now),
		EventType: &protos.HistoryEvent_ExecutionCompleted{
			ExecutionCompleted: &protos.ExecutionCompletedEvent{
				WorkflowStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED,
				FailureDetails: &protos.TaskFailureDetails{
					ErrorType:    staterrors.ErrorTypeHeldStartExpired,
					ErrorMessage: msg,
				},
			},
		},
	})
	if err := o.signAndSaveState(ctx, state); err != nil {
		return err
	}
	// The views were never primed for this state: reload on the next call.
	o.invalidateCachedState()

	return o.handleRetention(ctx, protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED, now)
}
