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
	"maps"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/events"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	staterrors "github.com/dapr/dapr/pkg/runtime/wfengine/state/errors"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
)

func (o *orchestrator) runWorkflow(ctx context.Context, reminder *actorapi.Reminder) (completed todo.RunCompleted, err error) {
	var state *wfenginestate.State
	state, _, err = o.loadInternalState(ctx)
	if err != nil {
		// Treat load failures as recoverable so the reminder is retried.
		// LoadWorkflowState already separates VerificationError (tombstoned
		// inside loadInternalState) from transient store failures. Anything
		// reaching here is a store-read or unmarshal failure — under
		// transient state-store pressure (latency, partial bulk response)
		// the same load will succeed on retry. Returning RunCompletedTrue
		// here would let the reminder system delete the wake-up handle and
		// strand the workflow.
		return todo.RunCompletedFalse, wferrors.NewRecoverable(fmt.Errorf("error loading internal state: %w", err))
	}
	if state == nil {
		// The assumption is that someone manually deleted the workflow state. This is non-recoverable.
		log.Warnf("No workflow state found for actor '%s', terminating execution", o.actorID)
		return todo.RunCompletedTrue, nil
	}
	// Read before the engine runs, which mutates the cached runtime state in
	// place: a notification is owed only by the turn that completes the
	// instance, so a later stray event on a settled child does not re-arm a
	// delivered one.
	wasCompleted := runtimestate.IsCompleted(o.rstate)

	if strings.HasPrefix(reminder.Name, "timer-") && !runtimestate.IsCompleted(o.rstate) {
		var durableTimer backend.DurableTimer
		if err = reminder.Data.UnmarshalTo(&durableTimer); err != nil {
			// Likely the result of an incompatible durable task timer format change.
			// This is non-recoverable.
			return todo.RunCompletedTrue, err
		}

		if durableTimer.GetGeneration() < state.Generation {
			log.Infof("Workflow actor '%s': ignoring durable timer from previous generation '%v'", o.actorID, durableTimer.GetGeneration())
			return todo.RunCompletedFalse, nil
		}

		timerEvent := durableTimer.GetTimerEvent()
		// Validate the timer event is actually a TimerFired event. A crafted
		// reminder could contain arbitrary event types to inject into the inbox.
		if timerEvent.GetTimerFired() == nil {
			return todo.RunCompletedTrue, fmt.Errorf("workflow actor '%s': timer reminder contains non-TimerFired event type %T", o.actorID, timerEvent.GetEventType())
		}
		// timer fired event is precreated at the moment of creating the timer
		// set the timestamp to now so it is accurately recorded in the history
		timerEvent.Timestamp = timestamppb.Now()
		state.Inbox = append(state.Inbox, timerEvent)
	}

	// A recursively-terminated parent delivers its cascade via this reminder,
	// carrying the ExecutionTerminated event as data (see terminateChildren).
	// Feed it into the inbox like a fired timer; if the workflow is already
	// terminal the redelivery is skipped and the reminder deleted.
	if reminder.Name == reminderCascadeTerminate && !runtimestate.IsCompleted(o.rstate) {
		var cascadeEvent backend.HistoryEvent
		if err = reminder.Data.UnmarshalTo(&cascadeEvent); err != nil {
			return todo.RunCompletedTrue, err
		}
		// Validate the event type. A crafted reminder could contain arbitrary
		// event types to inject into the inbox.
		if cascadeEvent.GetExecutionTerminated() == nil {
			return todo.RunCompletedTrue, fmt.Errorf("workflow actor '%s': cascade-terminate reminder contains non-ExecutionTerminated event type %T", o.actorID, cascadeEvent.GetEventType())
		}
		cascadeEvent.Timestamp = timestamppb.Now()
		state.Inbox = append(state.Inbox, &cascadeEvent)
	}

	if len(state.Inbox) == 0 && len(o.foldPending) == 0 {
		// The in-memory cache may be stale: during a placement cluster failure
		// daprds will roll over the actor, so a peer host may have written a new
		// inbox event to the store since our cache was last updated. Drop the
		// cache and reload from the store before declaring this a no-op. Acking
		// SUCCESS off a stale empty inbox would tell the scheduler to delete the
		// job and strand the workflow on the durable event that's actually sitting
		// in the store. A terminal cached rstate is reloaded too: on
		// instance-ID reuse the store may already hold the new generation's
		// pending start, and acking off the stale cache would delete its
		// reminder.
		o.invalidateCachedState()
		state, _, err = o.loadInternalState(ctx)
		if err != nil {
			return todo.RunCompletedFalse, wferrors.NewRecoverable(fmt.Errorf("failed to reload state on empty-inbox path: %w", err))
		}
		if state == nil {
			log.Warnf("No workflow state found for actor '%s' after reload, terminating execution", o.actorID)
			return todo.RunCompletedTrue, nil
		}
	}

	if len(state.Inbox) == 0 && len(o.foldPending) == 0 {
		// This can happen after multiple events are processed in batches; there
		// may still be reminders around for some of those already processed
		// events.
		// If the workflow is terminal, attempt retention reminder creation
		// idempotently. This recovers a workflow whose completion was persisted in
		// a prior run but whose retention reminder Create RPC was lost (e.g.
		// scheduler pod killed mid-call). createRetentionReminder uses a
		// deterministic name, so re-creating an already-existing retention
		// reminder is a no-op overwrite.
		// Read once: a re-send below saves, and a failed metadata refresh
		// after that save drops the cached runtime state.
		rst := o.rstate
		completed := runtimestate.IsCompleted(rst)
		if completed {
			// A failure nacks the driving reminder; any driver other than the
			// dedicated retry reminder also arms it.
			if serr := o.settleTerminal(ctx, state, rst, reminder.Name != reminderNameParentNotify); serr != nil {
				return todo.RunCompletedFalse, serr
			}
		}
		log.Debugf("Workflow actor '%s': ignoring run request for reminder '%s' because the workflow inbox is empty", o.actorID, reminder.Name)
		if o.fastPath && !completed {
			// Returning RunCompletedTrue deactivates the actor, and a
			// concurrent fold submit can append a held completion the moment
			// this no-op fire releases the lock: the deactivation would then
			// flush it into a sender retry (a spurious nack and a retry-long
			// stall). Keep the running actor resident; the actor runtime's
			// idle deactivation still bounds its lifetime.
			return todo.RunCompletedFalse, nil
		}
		return todo.RunCompletedTrue, nil
	}

	var esHistoryEvent *backend.HistoryEvent
	for _, e := range state.Inbox {
		if es := e.GetExecutionStarted(); es != nil {
			esHistoryEvent = e
			if esHistoryEvent.Router == nil {
				// Set the source app ID for cross-app routing in durabletask-go
				esHistoryEvent.Router = &protos.TaskRouter{
					SourceAppID: o.appID,
				}
			}
			break
		}
	}

	// A terminal rstate with a pending ExecutionStarted (or empty history)
	// means the cache trails a purge/recreate; running the turn would
	// resurrect the instance as PENDING with no name. Reload and retry.
	if runtimestate.IsCompleted(o.rstate) && (esHistoryEvent != nil || len(state.History) == 0) {
		log.Warnf("Workflow actor '%s': cached runtime state is terminal but the durable view holds a pending start (history len %d); reloading before running", o.actorID, len(state.History))
		o.invalidateCachedState()
		return todo.RunCompletedFalse, wferrors.NewRecoverable(fmt.Errorf("workflow actor '%s': inconsistent cached state (terminal runtime state with pending start), reloaded", o.actorID))
	}

	// Events but no ExecutionStarted anywhere: the committed start was lost
	// and the instance would report PENDING forever while its work is
	// silently dropped. Reclassify against durable truth first (the cache
	// may trail a peer host's committed start), then fail terminally.
	if esHistoryEvent == nil && len(state.History) == 0 {
		o.invalidateCachedState()
		state, _, err = o.loadInternalState(ctx)
		if err != nil {
			return todo.RunCompletedFalse, wferrors.NewRecoverable(fmt.Errorf("failed to reload state to classify unstartable inbox: %w", err))
		}
		if state == nil {
			log.Warnf("No workflow state found for actor '%s' after reload, terminating execution", o.actorID)
			return todo.RunCompletedTrue, nil
		}
		if isUnstartableState(state) {
			return o.failUnstartableWorkflow(ctx, state)
		}
		// Startable after all: retry against the reloaded view.
		return todo.RunCompletedFalse, wferrors.NewRecoverable(fmt.Errorf("workflow actor '%s': cached state held events with no ExecutionStarted but the durable state is startable; reloaded", o.actorID))
	}

	// Take any held completions into this turn (WorkflowsFastPath):
	// they ride the turn's single Multi into history and their senders are
	// acked only if that commit happens. Any outcome that does not commit
	// them nacks the senders back into their retry chains. Overflow beyond
	// the per-turn cap (and anything submitted after this take) re-arms a
	// drive so it is folded by a follow-up turn.
	folded := o.foldTake(state.Generation)
	foldedCommitted := false
	defer func() {
		if foldedCommitted {
			foldAck(folded)
		} else {
			foldNack(folded, err)
		}
		if len(o.foldPending) > 0 {
			p := o.foldPending[0].event
			o.localDrive(events.EventReminderName(reminderPrefixNewEvent, p), time.Now(), o.getExecutionStartedEvent(state).GetName())
		}
	}()

	rs := o.rstate
	newEvents := state.Inbox
	if len(folded) > 0 {
		newEvents = make([]*backend.HistoryEvent, 0, len(state.Inbox)+len(folded))
		newEvents = append(newEvents, state.Inbox...)
		for _, f := range folded {
			newEvents = append(newEvents, f.event)
		}
	}
	wi := &backend.WorkflowWorkItem{
		InstanceID: api.InstanceID(rs.GetInstanceId()),
		NewEvents:  newEvents,
		RetryCount: -1, // TODO
		State:      rs,
		Properties: make(map[string]any, 1),
	}

	wi.IncomingHistory = state.IncomingHistory

	workflowName := o.getExecutionStartedEvent(state).GetName()
	if reason, description, oversize := o.workflowPayloadOversize(ctx, state, foldedEvents(folded), workflowName); oversize {
		// Persist taken completions into the durable inbox before stalling:
		// a nacked fold dies with its sender's process, leaving the stall
		// unrecoverable once a restart lifts the limit (the janitor skips
		// stalled instances and the durable run-activity reminder was
		// elided). The inbox write is a state-store Multi, not an app call,
		// so the body limit does not apply; the janitor's pending-inbox arm
		// re-runs this turn each period and proceeds once the limit allows.
		if len(folded) > 0 {
			for _, f := range folded {
				state.AddToInbox(f.event)
			}
			if serr := o.signAndSaveState(ctx, state); serr != nil {
				return todo.RunCompletedFalse, serr
			}
			if jerr := o.ensureJanitor(ctx, state); jerr != nil {
				return todo.RunCompletedFalse, jerr
			}
			foldedCommitted = true
		}
		return todo.RunCompletedFalse, o.stallWorkflow(ctx, state, rs, reason, description)
	}
	// Executing workflow code is a one-way operation. We must wait for the app code to report its completion, which
	// will trigger this callback channel.
	callback := make(chan bool, 1)
	wi.Properties[todo.CallbackChannelProperty] = callback
	// Setting diagnoseStatus to failed by default to record metrics for non-recoverable errors.
	diagnoseStatus := diag.StatusFailed
	if rs != nil && runtimestate.IsCompleted(rs) {
		// If workflow is already completed, set executionStatus to empty string
		// which will skip recording metrics for this execution.
		diagnoseStatus = ""
	}
	// Request to execute workflow
	log.Debugf("Workflow actor '%s': scheduling workflow execution with instanceId '%s'", o.actorID, wi.InstanceID)
	// Schedule the workflow execution by signaling the backend.
	// The engine shares wi.State with o.rstate (same pointer) and may
	// overwrite it during ContinueAsNew (*s = *newState in the applier). The
	// failure paths below therefore invalidate the cached state instead of
	// restoring a snapshot: they all return recoverable errors, so the
	// refired reminder reloads durable truth from the store. This avoids
	// deep-cloning the entire history on every turn for a rollback that
	// almost never happens.

	// TODO: @joshvanl remove.
	err = o.scheduler(ctx, wi)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return todo.RunCompletedFalse, wferrors.NewRecoverable(fmt.Errorf("timed-out trying to schedule a workflow execution - this can happen if there are too many in-flight workflows or if the workflow engine isn't running: %w", err))
		}
		return todo.RunCompletedFalse, wferrors.NewRecoverable(fmt.Errorf("failed to schedule a workflow execution: %w", err))
	}

	o.recordWorkflowSchedulingLatency(ctx, esHistoryEvent, workflowName)
	wfExecutionElapsedTime := float64(0)

	defer func() {
		if diagnoseStatus != "" {
			diag.DefaultWorkflowMonitoring.WorkflowExecutionEvent(ctx, workflowName, diagnoseStatus)
			diag.DefaultWorkflowMonitoring.WorkflowExecutionLatency(ctx, workflowName, diagnoseStatus, wfExecutionElapsedTime)
		}
	}()

	select {
	case <-ctx.Done(): // caller is responsible for timeout management
		// The engine may have partially mutated o.rstate via the shared
		// wi.State pointer before the context was cancelled. Drop the cache
		// so the retry reloads from the store.
		o.invalidateCachedState()
		diagnoseStatus = diag.StatusRecoverable
		return todo.RunCompletedFalse, ctx.Err()
	case completed := <-callback:
		if !completed {
			// The engine abandoned this work item (e.g. MaxContinueAsNewCount
			// exceeded). The engine's ContinueAsNew tight-loop may have
			// overwritten o.rstate via the shared wi.State pointer
			// (*s = *newState in the applier). If CAN progress was made,
			// persist it to the state store so it survives actor
			// deactivation. Carryover events (unprocessed EventRaised
			// events from the CAN state) are moved to the Inbox so they
			// become NewEvents on retry. The stale inbox (which contained
			// ALL original events including those already consumed) is
			// replaced to prevent duplicate event delivery.
			// If no CAN progress was made (non-CAN failure), restore the
			// pre-engine snapshot so the cached state stays consistent.
			if wi.State.GetContinuedAsNew() {
				// Separate carryover EventRaised events from the CAN
				// execution events (WorkflowStarted, ExecutionStarted).
				// Carryover events must go into the Inbox so they become
				// NewEvents on retry. If they stay in History (as
				// OldEvents) alongside the original Inbox events
				// (NewEvents), the engine would buffer both sets and the
				// workflow would process duplicate events.
				canNewEvents := wi.State.GetNewEvents()
				filtered := make([]*backend.HistoryEvent, 0, len(canNewEvents))
				var carryover []*backend.HistoryEvent
				for _, e := range canNewEvents {
					if e.GetEventRaised() != nil {
						carryover = append(carryover, e)
					} else {
						filtered = append(filtered, e)
					}
				}

				// Temporarily swap NewEvents so ApplyRuntimeStateChanges
				// only writes the CAN execution events to History.
				if len(carryover) > 0 {
					wi.State.NewEvents = filtered
					state.ApplyRuntimeStateChanges(wi.State)
					wi.State.NewEvents = canNewEvents

					state.ClearInbox()
					for _, e := range carryover {
						state.AddToInbox(e)
					}
				} else {
					state.ApplyRuntimeStateChanges(wi.State)
				}

				state.Generation++

				// The engine carries the propagation chain across CAN by
				// updating wi.IncomingHistory. Persist any change so the new
				// generation observes the chain on its next run.
				if wi.IncomingHistory != state.IncomingHistory {
					state.SetIncomingHistory(wi.IncomingHistory)
				}

				// Save the carryover BEFORE creating its wake-up reminder,
				// mirroring AddWorkflowEvent: a reminder created first can
				// fire remotely against un-saved state, ack SUCCESS and be
				// deleted, stranding the carryover once the save commits.
				if err = o.signAndSaveState(ctx, state); err != nil {
					// signAndSaveState already invalidated the cache.
					return todo.RunCompletedFalse, err
				}
				// The CAN save persisted the effect of every consumed event,
				// including folded completions: their senders are acked.
				foldedCommitted = true

				// The generation bumped: void the escalation marks rather
				// than reap them (see reapEscalatedCompletions).
				o.reapEscalatedCompletions(state)

				// Bump before the elide so a stale escalation cannot
				// recreate the reminder.
				o.wakeEpoch.Add(1)

				// The save above durably committed the consumed
				// ExecutionStarted, so the pending start one-shot is a no-op
				// here exactly as on the normal commit path below: elide it.
				if esHistoryEvent != nil && o.fastPath {
					o.deleteStartReminder(esHistoryEvent)
				}

				if len(carryover) > 0 {
					reminderName := events.EventReminderName(reminderPrefixNewEvent, carryover[0])
					if o.fastPath {
						// Fast path: janitor + local drive instead of the
						// durable per-event reminder (falling back to it if
						// the janitor cannot be ensured). The subsequent
						// recoverable ErrExecutionAborted return also
						// propagates to the wake goroutine driving THIS
						// turn, whose escalation then re-arms a durable
						// reminder for the original event; that re-arm is
						// redundant with this drive but idempotent and
						// self-cleaning (empty-inbox ack).
						if jerr := o.ensureJanitor(ctx, state); jerr != nil {
							if err = o.createWorkflowReminder(ctx, reminderName, nil, time.Now(), o.appID, &workflowName); err != nil {
								return todo.RunCompletedFalse, wferrors.NewRecoverable(err)
							}
						}
						o.localDrive(reminderName, time.Now(), workflowName)
					} else {
						if err = o.createWorkflowReminder(ctx, reminderName, nil, time.Now(), o.appID, &workflowName); err != nil {
							// The carryover is already durable in the inbox; a
							// recoverable error FAILs this reminder invocation, so
							// the driving reminder refires and the reloaded state
							// re-runs the new generation normally. The cache is
							// consistent with the store post-save, so it is not
							// invalidated.
							return todo.RunCompletedFalse, wferrors.NewRecoverable(err)
						}
					}
				}
			} else {
				// Non-CAN abandon: the engine may have mutated the shared
				// rstate. Drop the cache so the retry reloads from the store.
				o.invalidateCachedState()
			}
			diagnoseStatus = diag.StatusRecoverable
			return todo.RunCompletedFalse, wferrors.NewRecoverable(todo.ErrExecutionAborted)
		}
	}
	rs = wi.State

	// The engine has mutated the shared runtime state through wi.State. From
	// here until a save settles cache consistency (signAndSaveState re-primes
	// the cache on success and invalidates it on failure), every exit must drop
	// the cache.
	cacheSettled := false
	defer func() {
		if !cacheSettled {
			o.invalidateCachedState()
		}
	}()

	if err = o.handleStalled(ctx, state, rs); err != nil {
		return todo.RunCompletedFalse, err
	}
	compactPatches(rs)
	o.stripUnmatchedResolutions(state, rs)

	// Reject a turn whose response provably came from a stale or duplicate
	// completion delivery  BEFORE any side effect.
	// The rejection is recoverable, so the driving reminder or wake retries the
	// turn; the retry re-registers its rendezvous, drains any displaced parked
	// response, and converges on the real one. ContinueAsNew turns are exempt:
	// the generation's history was replaced and IDs restart.
	if !rs.GetContinuedAsNew() {
		if kind, id, stale := staleTurnDuplicate(state, rs); stale {
			log.Warnf("Workflow actor '%s': rejecting turn whose response re-creates committed %s operation '%d' (stale completion delivery adopted across turns); retrying the turn", o.actorID, kind, id)
			diag.DefaultWorkflowMonitoring.WorkflowLocalWake(ctx, diag.StatusStaleTurnRejected)
			o.invalidateCachedState()
			diagnoseStatus = diag.StatusRecoverable
			return todo.RunCompletedFalse, wferrors.NewRecoverable(fmt.Errorf("turn response re-creates committed %s operation %d; stale completion delivery rejected", kind, id))
		}
	}

	runtimeStatus := runtimestate.RuntimeStatus(rs)
	log.Debugf("Workflow actor '%s': workflow execution returned with status '%s' instanceId '%s'", o.actorID, runtimeStatus.String(), wi.InstanceID)

	// Increment the generation counter if the workflow used continue-as-new. Subsequent actions below
	// will use this updated generation value for their duplication execution handling.
	if rs.GetContinuedAsNew() {
		log.Debugf("Workflow actor '%s': workflow with instanceId '%s' continued as new", o.actorID, wi.InstanceID)
		state.Generation += 1
		// The engine carries the propagation chain across CAN by updating
		// wi.IncomingHistory. Persist any change so the new generation sees
		// the chain on its next run.
		if wi.IncomingHistory != state.IncomingHistory {
			state.SetIncomingHistory(wi.IncomingHistory)
		}
	}

	if !runtimestate.IsCompleted(rs) {
		// Delete timer reminders for WaitForSingleEvent timers where the event has
		// been received before the timer fired.
		if err = o.deleteCancelledEventTimers(ctx, rs); err != nil {
			diagnoseStatus = diag.StatusRecoverable
			return todo.RunCompletedFalse, wferrors.NewRecoverable(err)
		}

		if err = o.createTimers(ctx, rs.GetPendingTimers(), state.Generation); err != nil {
			diagnoseStatus = diag.StatusRecoverable
			return todo.RunCompletedFalse, wferrors.NewRecoverable(err)
		}
	}

	pendingTasks := rs.GetPendingTasks()

	// Process the outbound orchestrator events.
	var createWorkflows []*backend.WorkflowRuntimeStateMessage
	for _, msg := range rs.GetPendingMessages() {
		switch {
		case msg.GetHistoryEvent().GetExecutionStarted() != nil:
			createWorkflows = append(createWorkflows, msg)

		case msg.GetHistoryEvent().GetChildWorkflowInstanceCompleted() != nil, msg.GetHistoryEvent().GetChildWorkflowInstanceFailed() != nil:
			// The completion owed to the parent. Not dispatched here: the
			// terminal save records that it is owed and settleTerminal
			// rebuilds it from the committed history, so the first send and
			// every re-send are the same message.

		case msg.GetHistoryEvent().GetExecutionTerminated() != nil && runtimestate.IsCompleted(rs):
			// Recursive-terminate cascade messages. Not dispatched here as this
			// runs before the terminal state is persisted; terminateChildren
			// delivers them after the save in the completion block below.

		default:
			return todo.RunCompletedTrue, fmt.Errorf("workflow actor '%s': don't know how to process outbound message '%v'", o.actorID, msg)
		}
	}

	// Attach a fresh chunk-local signature + cert chain to the current-app
	// chunk of every outbound PropagatedHistory so the receiver can
	// cryptographically verify the chunk against this app's identity.
	// Lineage chunks from upstream apps are forwarded verbatim. No-op
	// when signing is disabled.
	//
	// Two sources of outbound PropagatedHistory:
	//   - wi.OutgoingHistory: keyed by action ID, used for activity
	//     dispatch (callActivities).
	//   - createWorkflows[i].PropagatedHistory: per child-workflow
	//     creation message, consumed by callCreateWorkflowStateMessage.
	for _, ph := range wi.OutgoingHistory {
		if err = o.signing.SignOutgoingPropagatedHistory(ph, o.appID); err != nil {
			return todo.RunCompletedFalse, err
		}
	}
	for _, msg := range createWorkflows {
		if err = o.signing.SignOutgoingPropagatedHistory(msg.GetPropagatedHistory(), o.appID); err != nil {
			return todo.RunCompletedFalse, err
		}
	}

	// Dispatch activities and messages, collecting failures. Activity
	// dispatches certify the reminder elision only when the janitor backstop
	// is provably armed first (durability before the ack, mirroring
	// driveNewEvent); on janitor failure the dispatch degrades to the durable
	// run-activity reminder path.
	elideActivityReminder := o.fastPath && len(pendingTasks) > 0
	if elideActivityReminder {
		if jerr := o.ensureJanitor(ctx, state); jerr != nil {
			log.Warnf("Workflow actor '%s': failed to ensure janitor reminder, dispatching activities with durable reminders: %v", o.actorID, jerr)
			elideActivityReminder = false
		}
	}
	activityResult := o.callActivities(ctx, pendingTasks, state, rs, wi.OutgoingHistory, elideActivityReminder)
	createResult := o.messages.CallCreateWorkflowStateMessage(ctx, createWorkflows, rs.GetNewEvents())

	dispatchErr := errors.Join(activityResult.Err, createResult.Err)
	if dispatchErr != nil {
		if errors.Is(dispatchErr, errPayloadSizeExceeded) {
			return todo.RunCompletedFalse, o.stallWorkflow(ctx, state, rs,
				protos.StalledReason_PAYLOAD_SIZE_EXCEEDED, dispatchErr.Error())
		}
		if len(state.History) == 0 && (hasRemoteTasks(pendingTasks) || hasRemoteMessages(createWorkflows)) {
			// Save state without the events that failed to dispatch so the
			// workflow transitions to RUNNING. Successfully dispatched items
			// keep their events in history so they are not re-dispatched on
			// retry. The inbox is preserved so the existing reminder retries
			// the full execution.
			allFailed := make(map[int32]struct{}, len(activityResult.FailedEventIDs)+len(createResult.FailedEventIDs))
			maps.Copy(allFailed, activityResult.FailedEventIDs)
			maps.Copy(allFailed, createResult.FailedEventIDs)

			// Temporarily replace rs.NewEvents with a filtered copy that excludes
			// failed dispatch events, then restore the original after
			// ApplyRuntimeStateChanges. This works because ApplyRuntimeStateChanges
			// reads rs.NewEvents by reference (via GetNewEvents()) and appends
			// directly to state.History. It does not copy or retain the slice.
			origNewEvents := rs.NewEvents
			filtered := origNewEvents[:0:0]
			for _, e := range origNewEvents {
				if isDispatchableEvent(e) {
					if _, failed := allFailed[e.GetEventId()]; failed {
						continue
					}
				}
				filtered = append(filtered, e)
			}
			rs.NewEvents = filtered
			state.ApplyRuntimeStateChanges(rs)
			rs.NewEvents = origNewEvents
			saveErr := o.signAndSaveState(ctx, state)
			cacheSettled = true
			if saveErr != nil {
				return todo.RunCompletedFalse, saveErr
			}
			o.reapEscalatedCompletions(state)
			diagnoseStatus = diag.StatusRecoverable
			return todo.RunCompletedFalse, wferrors.NewRecoverable(dispatchErr)
		}

		diagnoseStatus = diag.StatusRecoverable
		return todo.RunCompletedFalse, wferrors.NewRecoverable(dispatchErr)
	}

	// A fastpath terminal turn may be driven by a local wake with no reminder
	// to nack, so the janitor is the durable driver for anything the turn
	// owes after the save: the parent notification, retention, the cascade.
	if o.fastPath && runtimestate.IsCompleted(rs) {
		if err = o.ensureJanitor(ctx, state); err != nil {
			return todo.RunCompletedFalse, wferrors.NewRecoverable(fmt.Errorf("failed to assert the janitor before the terminal save: %w", err))
		}
	}
	state.ApplyRuntimeStateChanges(rs)
	state.ClearInbox()
	if !wasCompleted && runtimestate.IsCompleted(rs) && o.getExecutionStartedEvent(state).GetParentInstance() != nil {
		state.SetParentNotifyPending(true)
	}

	err = o.signAndSaveState(ctx, state)
	cacheSettled = true
	if err != nil {
		return todo.RunCompletedFalse, err
	}
	// The turn's single Multi is durable: folded completions are now in
	// history and their senders are acked (see the deferred fold handling).
	foldedCommitted = true

	// Bump before the elide so a stale escalation cannot recreate the
	// reminder.
	o.wakeEpoch.Add(1)

	o.reapEscalatedCompletions(state)

	// This turn consumed the ExecutionStarted event and its commit above is
	// durable, so the pending start one-shot can only ever fire as a no-op:
	// elide it from the scheduler, detached and best-effort.
	if esHistoryEvent != nil && o.fastPath {
		o.deleteStartReminder(esHistoryEvent)
	}

	rstatus := runtimestate.RuntimeStatus(rs)
	if diagnoseStatus != "" {
		// If workflow is not completed, set executionStatus to empty string
		// which will skip recording metrics for this execution.
		diagnoseStatus = ""
		if runtimestate.IsCompleted(rs) {
			diagnoseStatus = executionStatusForRuntimeStatus(rstatus)
			wfExecutionElapsedTime = o.calculateWorkflowExecutionLatency(state)
		}
	}

	if runtimestate.IsCompleted(rs) {
		log.Infof("Workflow Actor '%s': workflow completed with status '%s' workflowName '%s'", o.actorID, rstatus, workflowName)
		// Everything after the save is idempotent and retried by the driving
		// reminder, or the janitor, on failure. Reminders are deleted after
		// it so a failure here keeps its retry.
		if err = o.settleTerminal(ctx, state, rs, true); err != nil {
			return todo.RunCompletedFalse, err
		}
		if hasUnfiredTimers(rs) {
			if err = o.deleteAllReminders(ctx); err != nil {
				return todo.RunCompletedFalse, err
			}
		} else if o.fastPath || o.janitorAsserted.Load() {
			// The repeating janitor does not self-clean on ack like the
			// one-shot reminders; remove it explicitly. janitorAsserted is
			// per-activation, so a janitor armed by a previous activation
			// would otherwise be skipped here; under the gate the delete is
			// attempted regardless (NotFound tolerated). Best-effort: a
			// missed delete self-deletes on its next fire against the
			// terminal state, and purge sweeps it on any binary version.
			o.deleteJanitor(ctx)
		}
		return todo.RunCompletedTrue, nil
	}

	return todo.RunCompletedFalse, nil
}

// executionStatusForRuntimeStatus maps a terminal workflow runtime status to
// the status label recorded on the workflow execution metrics. It is only
// meaningful for completed workflows. Completed maps to success and terminated
// to its own label; every other terminal status (in practice
// RUNTIME_STATUS_FAILED) is recorded as failed. The engine never assigns
// RUNTIME_STATUS_CANCELED to a top-level orchestration, so cancelled is
// unreachable; the default arm keeps any unexpected future terminal status
// accounted for rather than silently dropped.
func executionStatusForRuntimeStatus(status api.OrchestrationStatus) string {
	switch status {
	case api.RUNTIME_STATUS_COMPLETED:
		return diag.StatusSuccess
	case api.RUNTIME_STATUS_TERMINATED:
		return diag.StatusTerminated
	default:
		return diag.StatusFailed
	}
}

func (*orchestrator) calculateWorkflowExecutionLatency(state *wfenginestate.State) (wExecutionElapsedTime float64) {
	for _, e := range state.History {
		if os := e.GetWorkflowStarted(); os != nil {
			return diag.ElapsedSince(e.GetTimestamp().AsTime())
		}
	}
	return 0
}

func (*orchestrator) recordWorkflowSchedulingLatency(ctx context.Context, esHistoryEvent *backend.HistoryEvent, workflowName string) {
	if esHistoryEvent == nil {
		return
	}

	// If the event is an execution started event, then we need to record the scheduled start timestamp
	if es := esHistoryEvent.GetExecutionStarted(); es != nil {
		currentTimestamp := time.Now()
		var scheduledStartTimestamp time.Time
		timestamp := es.GetScheduledStartTimestamp()

		if timestamp != nil {
			scheduledStartTimestamp = timestamp.AsTime()
		} else {
			// if scheduledStartTimestamp is nil, then use the event timestamp to consider scheduling latency
			// This case will happen when the workflow is created and started immediately
			scheduledStartTimestamp = esHistoryEvent.GetTimestamp().AsTime()
		}

		wfSchedulingLatency := float64(currentTimestamp.Sub(scheduledStartTimestamp).Milliseconds())
		diag.DefaultWorkflowMonitoring.WorkflowSchedulingLatency(ctx, workflowName, wfSchedulingLatency)
	}
}

// retentionReminderName is the single deterministic name used for every
// retention reminder. Keeping the name constant (rather than keying it on
// the terminal status) ensures the scheduler's overwrite-by-name semantics
// collapse re-scheduled runs of the same instance ID onto one retention
// reminder even if the terminal status differs between runs (e.g. run 1
// completed, run 2 terminated). The workflow's actual status is still
// recoverable via FetchWorkflowMetadata; only the scheduler key is now
// status-agnostic.
const retentionReminderName = "retention"

// handleRetention creates the retention reminder for a terminal workflow.
// The reminder name is deterministic, so this is safe to call repeatedly:
// the scheduler overwrites by name, leaving exactly one retention reminder
// per workflow. The dueTime is anchored to the workflow's completion time
// (not time.Now()) so retries on a transient scheduler failure converge to
// the same dueTime instead of pushing retention back on every attempt.
//
// One one-shot per instance is deliberate, not batched per app: this
// reminder is idempotently re-creatable from the instance's own durable
// state (the empty-inbox completion path above re-asserts it after a lost
// Create), while a shared per-appID bucket job would need a read-modify-write
// of job data that is not atomic with the completion save, with no durable
// per-instance anchor (and no completion-time index) to recover a lost join.
// settleTerminal runs what a completed instance owes after its terminal
// commit, idempotently, so every driver (the terminal turn, an empty-inbox
// fire, the janitor) leaves the same state behind: the pending parent
// notification, the retention reminder and the recursive terminate. rst is
// read before the re-send, whose save may drop the cached runtime state.
func (o *orchestrator) settleTerminal(ctx context.Context, state *wfenginestate.State, rst *protos.WorkflowRuntimeState, arm bool) error {
	status := runtimestate.RuntimeStatus(rst)
	completedAt, err := runtimestate.CompletedTime(rst)
	if err != nil || completedAt.IsZero() {
		// Reported terminal without a completion time: fall back to now so
		// the retention reminder is still scheduled rather than dropped.
		completedAt = time.Now()
	}
	// Retention and the cascade first: an unreachable parent must not hold
	// them back, and both are idempotent re-asserts.
	if err = o.handleRetention(ctx, status, completedAt); err != nil {
		return wferrors.NewRecoverable(fmt.Errorf("failed to (re)create the retention reminder: %w", err))
	}
	if err = o.terminateChildren(ctx, state); err != nil {
		return wferrors.NewRecoverable(fmt.Errorf("failed to (re)deliver the recursive terminate to children: %w", err))
	}
	if state.ParentNotifyPending {
		return o.resendParentNotification(ctx, state, arm)
	}
	return nil
}

func (o *orchestrator) handleRetention(ctx context.Context, status protos.OrchestrationStatus, completedAt time.Time) error {
	if o.retentionPolicy == nil {
		return nil
	}

	var dueTime *time.Duration
	switch {
	case o.retentionPolicy.Completed != nil &&
		status == protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED:
		dueTime = o.retentionPolicy.Completed
	case o.retentionPolicy.Terminated != nil &&
		status == protos.OrchestrationStatus_ORCHESTRATION_STATUS_TERMINATED:
		dueTime = o.retentionPolicy.Terminated
	case o.retentionPolicy.Failed != nil &&
		status == protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED:
		dueTime = o.retentionPolicy.Failed
	case o.retentionPolicy.AnyTerminal != nil:
		dueTime = o.retentionPolicy.AnyTerminal
	}

	if dueTime == nil {
		return nil
	}

	log.Debugf("Workflow actor '%s': setting retention reminder for status '%s' with due time '%v'", o.actorID, status.String(), dueTime)
	_, err := o.createRetentionReminder(ctx, retentionReminderName, completedAt.Add(*dueTime))
	return err
}

// staleTurnDuplicate reports whether rs.NewEvents re-creates an operation
// (task, timer, or child workflow) whose creation event already exists in the
// committed history with the same event ID.
func staleTurnDuplicate(state *wfenginestate.State, rs *backend.WorkflowRuntimeState) (string, int32, bool) {
	kindOf := func(e *backend.HistoryEvent) string {
		switch {
		case e.GetTaskScheduled() != nil:
			return "task"
		case e.GetTimerCreated() != nil:
			return "timer"
		case e.GetChildWorkflowInstanceCreated() != nil:
			return "child"
		default:
			return ""
		}
	}

	created := make(map[string]struct{})
	var have bool
	for _, e := range rs.GetNewEvents() {
		if k := kindOf(e); k != "" {
			created[k+"/"+strconv.FormatInt(int64(e.GetEventId()), 10)] = struct{}{}
			have = true
		}
	}
	if !have {
		return "", 0, false
	}

	for _, e := range state.History {
		k := kindOf(e)
		if k == "" {
			continue
		}
		if _, ok := created[k+"/"+strconv.FormatInt(int64(e.GetEventId()), 10)]; ok {
			return k, e.GetEventId(), true
		}
	}
	return "", 0, false
}

// stripUnmatchedResolutions removes from rs.NewEvents any task or child
// workflow resolution event that resolves nothing: no matching TaskScheduled
// or ChildWorkflowInstanceCreated with the same event ID exists in persisted
// history or among this execution's new events. The app-side SDK silently
// ignores such events, so without this they would be persisted into history
// with no effect, where they poison dedup.IsDuplicateCompletion for a later
// operation that legitimately reuses the same event ID (the ID sequence resets
// on ContinueAsNew, so a straggler completion from an abandoned
// previous-generation child collides with the current generation's
// operations). Timer events are not stripped: stale timer firings are already
// rejected by the generation check on the timer reminder path.
func (o *orchestrator) stripUnmatchedResolutions(state *wfenginestate.State, rs *backend.WorkflowRuntimeState) {
	scheduledTaskIDs := make(map[int32]struct{})
	createdChildIDs := make(map[int32]struct{})
	index := func(events []*backend.HistoryEvent) {
		for _, e := range events {
			switch {
			case e.GetTaskScheduled() != nil:
				scheduledTaskIDs[e.GetEventId()] = struct{}{}
			case e.GetChildWorkflowInstanceCreated() != nil:
				createdChildIDs[e.GetEventId()] = struct{}{}
			}
		}
	}
	index(state.History)
	index(rs.GetNewEvents())

	matched := func(e *backend.HistoryEvent) bool {
		switch {
		case e.GetTaskCompleted() != nil:
			_, ok := scheduledTaskIDs[e.GetTaskCompleted().GetTaskScheduledId()]
			return ok
		case e.GetTaskFailed() != nil:
			_, ok := scheduledTaskIDs[e.GetTaskFailed().GetTaskScheduledId()]
			return ok
		case e.GetChildWorkflowInstanceCompleted() != nil:
			_, ok := createdChildIDs[e.GetChildWorkflowInstanceCompleted().GetTaskScheduledId()]
			return ok
		case e.GetChildWorkflowInstanceFailed() != nil:
			_, ok := createdChildIDs[e.GetChildWorkflowInstanceFailed().GetTaskScheduledId()]
			return ok
		default:
			return true
		}
	}

	events := rs.GetNewEvents()
	for _, e := range events {
		if matched(e) {
			continue
		}

		// At least one orphan: rebuild into a fresh backing array so callers
		// holding the original slice are unaffected.
		filtered := make([]*backend.HistoryEvent, 0, len(events)-1)
		for _, ev := range events {
			if !matched(ev) {
				log.Warnf("Workflow actor '%s': discarding resolution event %T that matches no operation scheduled in persisted history or in this execution (stale event from a previous generation?)", o.actorID, ev.GetEventType())
				continue
			}
			filtered = append(filtered, ev)
		}
		rs.NewEvents = filtered
		return
	}
}

// filterValidInboxEvents returns inbox events that pass validation. Result
// events (TaskCompleted/TaskFailed, ChildWorkflowInstanceCompleted/Failed)
// must match operations that were scheduled in signed history. Invalid events
// are dropped and logged.
func filterValidInboxEvents(state *wfenginestate.State) []*backend.HistoryEvent {
	// Build sets of scheduled operation event IDs from history.
	scheduledTaskIDs := make(map[int32]struct{})
	createdChildIDs := make(map[int32]struct{})
	for _, e := range state.History {
		switch {
		case e.GetTaskScheduled() != nil:
			scheduledTaskIDs[e.GetEventId()] = struct{}{}
		case e.GetChildWorkflowInstanceCreated() != nil:
			createdChildIDs[e.GetEventId()] = struct{}{}
		}
	}

	valid := make([]*backend.HistoryEvent, 0, len(state.Inbox))
	for _, e := range state.Inbox {
		// exhaustive linter will error here on missing types not implemented on
		// the switch.
		switch et := e.GetEventType().(type) {
		case *protos.HistoryEvent_TaskCompleted:
			taskID := et.TaskCompleted.GetTaskScheduledId()
			if _, ok := scheduledTaskIDs[taskID]; !ok {
				log.Warnf("Dropping injected inbox event: task result for task %d not scheduled in signed history", taskID)
				continue
			}
		case *protos.HistoryEvent_TaskFailed:
			taskID := et.TaskFailed.GetTaskScheduledId()
			if _, ok := scheduledTaskIDs[taskID]; !ok {
				log.Warnf("Dropping injected inbox event: task failure for task %d not scheduled in signed history", taskID)
				continue
			}
		case *protos.HistoryEvent_ChildWorkflowInstanceCompleted:
			taskID := et.ChildWorkflowInstanceCompleted.GetTaskScheduledId()
			if _, ok := createdChildIDs[taskID]; !ok {
				log.Warnf("Dropping injected inbox event: child workflow result for task %d not created in signed history", taskID)
				continue
			}
		case *protos.HistoryEvent_ChildWorkflowInstanceFailed:
			taskID := et.ChildWorkflowInstanceFailed.GetTaskScheduledId()
			if _, ok := createdChildIDs[taskID]; !ok {
				log.Warnf("Dropping injected inbox event: child workflow failure for task %d not created in signed history", taskID)
				continue
			}
		case *protos.HistoryEvent_EventRaised,
			*protos.HistoryEvent_TimerFired,
			*protos.HistoryEvent_ExecutionStarted,
			*protos.HistoryEvent_ExecutionTerminated,
			*protos.HistoryEvent_ExecutionResumed,
			*protos.HistoryEvent_ExecutionSuspended:
			// Legitimate inbox event types that do not correspond to a previously
			// scheduled operation.
		default:
			// DetachedWorkflowInstanceCreated is intentionally NOT in the
			// allow-list above: it is only ever produced by the caller's own
			// applier and persisted into the caller's history directly, so it
			// should never appear in an inbox. If it does, treat it as
			// injected and drop it.
			log.Warnf("Dropping injected inbox event: unknown event type %T", et)
			continue
		}
		valid = append(valid, e)
	}

	return valid
}

// isUnstartableState reports whether the durable state can never progress:
// inbox events with an empty history and no pending ExecutionStarted. The
// shape only arises when the committed start was lost.
func isUnstartableState(state *wfenginestate.State) bool {
	if len(state.Inbox) == 0 || len(state.History) != 0 {
		return false
	}
	for _, e := range state.Inbox {
		if e.GetExecutionStarted() != nil {
			return false
		}
	}
	return true
}

// failUnstartableWorkflow commits a FAILED completion describing the dropped
// inbox events, drains the inbox, and acks the driving reminder so
// redelivery stops.
func (o *orchestrator) failUnstartableWorkflow(ctx context.Context, state *wfenginestate.State) (todo.RunCompleted, error) {
	kinds := make([]string, 0, len(state.Inbox))
	for _, e := range state.Inbox {
		kinds = append(kinds, fmt.Sprintf("%T", e.GetEventType()))
	}
	msg := fmt.Sprintf("workflow instance holds %d inbox event(s) (%s) but an empty history and no pending ExecutionStarted; the committed start event was lost and the instance can never progress",
		len(state.Inbox), strings.Join(kinds, ", "))
	log.Errorf("Workflow actor '%s': %s; failing the workflow instance", o.actorID, msg)

	// RuntimeStatus reports PENDING whenever the start event is missing,
	// so a synthetic ExecutionStarted must precede the FAILED completion
	// for it to surface. The original start is lost; only the ID is known.
	state.AddToHistory(&backend.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{
				WorkflowInstance: &protos.WorkflowInstance{
					InstanceId: o.actorID,
				},
			},
		},
	})
	state.AddToHistory(&backend.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionCompleted{
			ExecutionCompleted: &protos.ExecutionCompletedEvent{
				WorkflowStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED,
				FailureDetails: &protos.TaskFailureDetails{
					ErrorType:    staterrors.ErrorTypeUnstartableState,
					ErrorMessage: msg,
				},
			},
		},
	})
	state.ClearInbox()
	if err := o.signAndSaveState(ctx, state); err != nil {
		return todo.RunCompletedFalse, err
	}
	diag.DefaultWorkflowMonitoring.WorkflowLocalWake(ctx, diag.StatusUnstartableFailed)
	if err := o.handleRetention(ctx, protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED, time.Now()); err != nil {
		return todo.RunCompletedFalse, wferrors.NewRecoverable(err)
	}
	return todo.RunCompletedTrue, nil
}
