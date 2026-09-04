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
	return o.createIfCompleted(ctx, o.rstate, state, startEvent, propagatedHistory,
		createWorkflowInstanceRequest.GetEnforceUniqueInstanceId())
}

func (o *orchestrator) createIfCompleted(ctx context.Context, rs *backend.WorkflowRuntimeState, state *wfenginestate.State, startEvent *backend.HistoryEvent, propagatedHistory *protos.PropagatedHistory, enforceUnique bool) error {
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
			// If the child saved its state but its start reminder was never
			// armed (the save-first create failed), this retry is the only
			// driver: re-assert idempotently from the SAVED event.
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

		// A saved-but-never-run instance whose start reminder was never armed
		// (the save-first create failed) would otherwise be permanently
		// stranded: clients retrying the create would only ever see
		// AlreadyExists. When the incoming create describes the same logical
		// start AND the reminder is genuinely missing, re-assert it from the
		// SAVED inbox event (the incoming event has a regenerated timestamp,
		// so the deterministic name must come from the saved one) and report
		// success. The reminder-missing check is what distinguishes a
		// stranded start from a concurrent duplicate create of an identical
		// workflow, which must keep failing with AlreadyExists: actor
		// invocations serialize, so a healthy duplicate always observes the
		// first create's reminder.
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

	// The create asked for instance ID uniqueness: a completed instance blocks
	// recreation just like an active one. The recovery paths above stay honoured
	// regardless, as they are idempotent retries of the same create, not new
	// creates.
	if enforceUnique {
		return status.Errorf(codes.AlreadyExists, "a workflow with ID '%s' already exists and the create enforces instance ID uniqueness", o.actorID)
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
	// Save the inbox event BEFORE creating the wake-up reminder, mirroring
	// AddWorkflowEvent. With the reminder created first, under placement
	// rebalance the reminder can fire on another host before the save
	// commits: it loads nil/stale state, acks SUCCESS, and the scheduler
	// deletes the reminder. Once the save then commits, the workflow is
	// stranded in RUNNING with no driver.
	//
	// Saving first inverts the failure into one that is recoverable: if the
	// save succeeds but the reminder Create fails, the error surfaces to the
	// caller, and any retry of the create lands in createIfCompleted's
	// pending-start path, which re-asserts the reminder by its deterministic
	// name derived from the saved inbox event.
	//
	// The reminder schedules the actual workflow execution rather than
	// running it on this thread, so the client is not blocked while the
	// workflow logic runs.
	state.AddToInbox(startEvent)
	if err := o.signAndSaveState(ctx, state); err != nil {
		return err
	}

	return o.assertStartReminder(ctx, startEvent)
}

// pendingStartEvent returns the ExecutionStarted inbox event of a workflow
// that was created (state durably saved) but has never executed a turn:
// history is empty and the inbox holds an ExecutionStarted. The inbox may
// also hold EventRaised rows (RaiseEvent against a pending instance), so only
// the presence of the ExecutionStarted is required. Returns nil otherwise.
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
// is absent from the scheduler. Get errors are propagated so a transient
// scheduler failure surfaces as a retryable error to the caller rather than
// either a spurious re-drive or a permanent-looking AlreadyExists.
func (o *orchestrator) startReminderMissing(ctx context.Context, saved *backend.HistoryEvent) (bool, error) {
	rem, err := o.reminders.Get(ctx, &actorapi.GetReminderRequest{
		Name:      events.EventReminderName(reminderPrefixStart, saved),
		ActorType: o.actorTypeBuilder.Workflow(o.appID),
		ActorID:   o.actorID,
	})
	if err != nil {
		// The contract is (nil, nil) for a missing reminder, but tolerate a
		// client surfacing NotFound as an error: treating it as retryable
		// would strand the pending instance permanently.
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return true, nil
		}
		return false, fmt.Errorf("failed to check for pending start reminder: %w", err)
	}
	return rem == nil, nil
}

// isSameLogicalStart reports whether an incoming ExecutionStarted event
// describes the same logical creation as the saved pending one. Per-attempt
// volatile fields (Timestamp, the child's own WorkflowInstance.ExecutionId,
// trace context) are ignored: a client retry of the same logical create
// regenerates them. The parent's ExecutionId inside ParentInstanceInfo is NOT
// volatile (it is persisted in the parent's own history and changes only on
// ContinueAsNew or recreate), so it is compared when present on both sides. A
// mismatch means a genuinely conflicting create, which keeps today's
// AlreadyExists behavior.
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
