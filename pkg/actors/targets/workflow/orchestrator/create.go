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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/events"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
)

func (o *orchestrator) createWorkflowInstance(ctx context.Context, request []byte) error {
	var createWorkflowInstanceRequest backend.CreateWorkflowInstanceRequest
	if err := proto.Unmarshal(request, &createWorkflowInstanceRequest); err != nil {
		return fmt.Errorf("failed to unmarshal createWorkflowInstanceRequest: %w", err)
	}

	startEvent := createWorkflowInstanceRequest.GetStartEvent()
	if es := startEvent.GetExecutionStarted(); es == nil {
		return errors.New("invalid execution start event")
	} else {
		if es.GetParentInstance() == nil {
			log.Debugf("Workflow actor '%s': creating workflow '%s' with instanceId '%s'",
				o.actorID,
				es.GetName(),
				es.GetWorkflowInstance().GetInstanceId(),
			)
		} else {
			log.Debugf("Workflow actor '%s': creating child workflow '%s' with instanceId '%s' parentWorkflow '%s' parentWorkflowId '%s'",
				o.actorID,
				es.GetName(),
				es.GetWorkflowInstance().GetInstanceId(),
				es.GetParentInstance().GetName(),
				es.GetParentInstance().GetWorkflowInstance().GetInstanceId(),
			)
		}
	}

	state, _, err := o.loadInternalState(ctx)
	if err != nil {
		return err
	}

	propagatedHistory := createWorkflowInstanceRequest.GetPropagatedHistory()

	// orchestration didn't exist
	// create a new state entry if one doesn't already exist
	if state == nil {
		state = wfenginestate.NewState(wfenginestate.Options{
			AppID:             o.appID,
			Namespace:         o.namespace,
			WorkflowActorType: o.actorType,
			ActivityActorType: o.activityActorType,
		})
		o.rstate = runtimestate.NewWorkflowRuntimeState(o.actorID, state.CustomStatus, state.History)
		o.ometa = o.ometaFromState(o.rstate, startEvent.GetExecutionStarted())

		if propagatedHistory != nil {
			if err := o.signing.VerifyAndAbsorbPropagatedHistory(propagatedHistory, state); err != nil {
				return fmt.Errorf("workflow actor '%s': propagated history verification failed: %w", o.actorID, err)
			}
			state.SetIncomingHistory(propagatedHistory)
		}
		return o.scheduleWorkflowStart(ctx, startEvent, state)
	}

	// orchestration already existed: create instance only if previous one is completed
	return o.createIfCompleted(ctx, o.rstate, state, startEvent, propagatedHistory)
}

func (o *orchestrator) createIfCompleted(ctx context.Context, rs *backend.WorkflowRuntimeState, state *wfenginestate.State, startEvent *backend.HistoryEvent, propagatedHistory *protos.PropagatedHistory) error {
	// We block (re)creation of existing workflows unless they are in a completed state
	// Or if they still have any pending activity result awaited.
	if !runtimestate.IsCompleted(rs) {
		pending := pendingStartEvent(state)

		// This happens when the parent's runWorkflow created the child workflow
		// successfully but crashed before persisting its own state, causing it to
		// re-execute and attempt the child creation again.
		sameParent, parentExecMismatch := o.isSameParentCreation(state, startEvent)
		if sameParent {
			log.Debugf("Workflow actor '%s': ignoring duplicate child workflow creation from parent '%s'",
				o.actorID, startEvent.GetExecutionStarted().GetParentInstance().GetWorkflowInstance().GetInstanceId())
			// A child that saved but never armed its start reminder has no
			// other driver: re-assert from the saved event.
			if pending != nil {
				missing, err := o.startReminderMissing(ctx, pending)
				if err != nil {
					return err
				}
				if missing {
					return o.assertStartReminder(ctx, pending)
				}
			}
			return nil
		}

		// A saved-but-never-run instance with no armed start reminder is
		// stranded: retries of the same logical create re-assert it from the
		// SAVED event (the incoming one has a regenerated timestamp). The
		// reminder-missing check keeps healthy duplicate creates failing
		// with AlreadyExists, since a duplicate always observes the first
		// create's reminder.
		if pending != nil && isSameLogicalStart(pending.GetExecutionStarted(), startEvent.GetExecutionStarted()) {
			missing, err := o.startReminderMissing(ctx, pending)
			if err != nil {
				return err
			}
			if missing {
				log.Infof("Workflow actor '%s': re-driving pending start for saved-but-never-run workflow", o.actorID)
				return o.assertStartReminder(ctx, pending)
			}
		}

		if parentExecMismatch {
			return status.Errorf(codes.AlreadyExists,
				"an active workflow with ID '%s' already exists: it was created by a previous execution of parent workflow '%s' (e.g. before a ContinueAsNew); child workflow instance IDs must be unique across parent executions",
				o.actorID, startEvent.GetExecutionStarted().GetParentInstance().GetWorkflowInstance().GetInstanceId())
		}

		return status.Errorf(codes.AlreadyExists, "an active workflow with ID '%s' already exists", o.actorID)
	}

	if o.activityResultAwaited.Load() {
		return fmt.Errorf("a terminated workflow with ID '%s' is already awaiting an activity result", o.actorID)
	}

	// An ID is reusable only once the previous execution's entire child
	// workflow tree is terminal: a still-running descendant could deliver
	// events from the old execution into the new one.
	if err := o.childrenTerminalCheck(ctx, state); err != nil {
		// AlreadyExists only for the genuine not-terminal verdict. A failure
		// to verify (child unreachable, context timeout) is Unavailable so
		// callers retry rather than treat the ID as taken; reuse stays
		// blocked either way.
		code := codes.Unavailable
		if strings.HasSuffix(err.Error(), api.ErrNotCompleted.Error()) {
			code = codes.AlreadyExists
		}
		return status.Errorf(code, "cannot recreate workflow with ID '%s': %s", o.actorID, err.Error())
	}

	log.Infof("Workflow actor '%s': workflow was previously completed and is being recreated", o.actorID)

	state.Reset()

	if propagatedHistory != nil {
		if err := o.signing.VerifyAndAbsorbPropagatedHistory(propagatedHistory, state); err != nil {
			return fmt.Errorf("workflow actor '%s': propagated history verification failed: %w", o.actorID, err)
		}
		state.SetIncomingHistory(propagatedHistory)
	}

	return o.scheduleWorkflowStart(ctx, startEvent, state)
}

func (o *orchestrator) scheduleWorkflowStart(ctx context.Context, startEvent *backend.HistoryEvent, state *wfenginestate.State) error {
	// Save BEFORE creating the wake-up reminder, mirroring AddWorkflowEvent:
	// a reminder created first can fire on another host before the save
	// commits, ack SUCCESS off the empty inbox and be deleted, stranding the
	// workflow once the save lands. Saving first makes the failure
	// recoverable: a failed reminder create surfaces to the caller, and a
	// create retry re-asserts it by deterministic name from the saved event.
	state.AddToInbox(startEvent)
	if err := o.signAndSaveState(ctx, state); err != nil {
		return err
	}

	return o.assertStartReminder(ctx, startEvent)
}

// pendingStartEvent returns the ExecutionStarted inbox event of a saved but
// never-run workflow (empty history), or nil. Other inbox rows (pre-start
// RaiseEvent) are ignored.
func pendingStartEvent(state *wfenginestate.State) *backend.HistoryEvent {
	if len(state.History) > 0 {
		return nil
	}
	for _, e := range state.Inbox {
		if e.GetExecutionStarted() != nil {
			return e
		}
	}
	return nil
}

// startReminderMissing reports whether the pending start's wake-up reminder
// is absent from the scheduler; Get errors surface as retryable.
func (o *orchestrator) startReminderMissing(ctx context.Context, saved *backend.HistoryEvent) (bool, error) {
	rem, err := o.reminders.Get(ctx, &actorapi.GetReminderRequest{
		Name:      events.EventReminderName(reminderPrefixStart, saved),
		ActorType: o.actorTypeBuilder.Workflow(o.appID),
		ActorID:   o.actorID,
	})
	if err != nil {
		// Missing is contractually (nil, nil), but tolerate NotFound-as-error.
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return true, nil
		}
		return false, fmt.Errorf("failed to check for pending start reminder: %w", err)
	}
	return rem == nil, nil
}

// isSameLogicalStart reports whether the incoming ExecutionStarted describes
// the same logical creation as the saved pending one, ignoring per-attempt
// volatile fields (Timestamp, own ExecutionId, trace context). The parent's
// ExecutionId is not volatile and is compared when present on both sides.
func isSameLogicalStart(saved, incoming *protos.ExecutionStartedEvent) bool {
	if saved.GetName() != incoming.GetName() {
		return false
	}

	if saved.GetVersion().GetValue() != incoming.GetVersion().GetValue() {
		return false
	}

	if saved.GetInput().GetValue() != incoming.GetInput().GetValue() {
		return false
	}

	savedTS, incomingTS := saved.GetScheduledStartTimestamp(), incoming.GetScheduledStartTimestamp()
	if (savedTS == nil) != (incomingTS == nil) {
		return false
	}
	if savedTS != nil && !savedTS.AsTime().Equal(incomingTS.AsTime()) {
		return false
	}

	savedParent, incomingParent := saved.GetParentInstance(), incoming.GetParentInstance()
	if (savedParent == nil) != (incomingParent == nil) {
		return false
	}
	if savedParent != nil {
		if savedParent.GetWorkflowInstance().GetInstanceId() != incomingParent.GetWorkflowInstance().GetInstanceId() ||
			savedParent.GetTaskScheduledId() != incomingParent.GetTaskScheduledId() ||
			!sameParentExecution(savedParent, incomingParent) {
			return false
		}
	}

	return true
}

// isSameParentCreation reports whether the incoming child creation is a
// crash-replay duplicate of the creation that produced the existing state.
// parentExecMismatch is true when the parent lineage and task slot match but
// the parent's ExecutionId differs: the parent continued-as-new (or was
// recreated) and scheduled a child whose instance ID collides with a live
// child of a previous execution. That is a genuinely new creation, not a
// replay, and must not be silently deduplicated.
func (o *orchestrator) isSameParentCreation(state *wfenginestate.State, startEvent *backend.HistoryEvent) (sameCreation bool, parentExecMismatch bool) {
	newParent := startEvent.GetExecutionStarted().GetParentInstance()
	if newParent == nil {
		return false, false
	}

	existingParent := o.getExecutionStartedEvent(state).GetParentInstance()
	if existingParent == nil {
		return false, false
	}

	if existingParent.GetWorkflowInstance().GetInstanceId() != newParent.GetWorkflowInstance().GetInstanceId() ||
		existingParent.GetTaskScheduledId() != newParent.GetTaskScheduledId() {
		return false, false
	}

	if !sameParentExecution(existingParent, newParent) {
		return false, true
	}

	return true, false
}

// sameParentExecution reports whether two parent references could belong to
// the same parent execution. A nil ExecutionId on EITHER side (older
// persisted state, rerun-path creations that omit it) conservatively returns
// true, preserving crash-replay dedup. Only both-set-and-different returns
// false.
func sameParentExecution(existing, incoming *protos.ParentInstanceInfo) bool {
	a := existing.GetWorkflowInstance().GetExecutionId()
	b := incoming.GetWorkflowInstance().GetExecutionId()
	return a == nil || b == nil || a.GetValue() == b.GetValue()
}
