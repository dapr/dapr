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

	"google.golang.org/protobuf/proto"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/dedup"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/signing"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	staterrors "github.com/dapr/dapr/pkg/runtime/wfengine/state/errors"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
)

const (
	reminderPrefixStart    = "start"
	reminderPrefixNewEvent = "new-event"
	reminderPrefixTimer    = "timer-"
	// Created on each child workflow actor by a recursively-terminated parent,
	// carrying the ExecutionTerminated event as reminder data.
	reminderCascadeTerminate = "cascade-terminate"
)

// completionSender identifies the child delivering a completion: its instance
// ID and the parent execution it was created under. Zero for senders that
// carry neither.
type completionSender struct {
	instanceID        string
	parentExecutionID string
}

// addWorkflowEvent appends an inbound event to the inbox and drives it.
func (o *orchestrator) addWorkflowEvent(ctx context.Context, e *backend.HistoryEvent, sender completionSender) error {
	state, _, err := o.loadInternalState(ctx)
	if err != nil {
		return err
	}

	if state == nil {
		log.Errorf("Workflow actor '%s': cannot add event to workflow as state has been purged. Ignoring event.", o.actorID)
		return api.ErrInstanceNotFound
	}

	// On a tombstoned workflow (cold-store load tamper or attestation
	// verification failure - identified by the unsigned tamper marker at
	// the end of history) reject inbound activity / child-workflow
	// completion events with ErrInstanceNotFound. The activity actor and
	// the child workflow's completion dispatch treat ErrInstanceNotFound
	// as terminal and stop re-delivering, so we don't loop the parent's
	// actor lock against a workflow that will never accept the result.
	// Other event types (RaiseEvent, terminate, etc.) still flow through.
	isCompletion := e.GetTaskCompleted() != nil || e.GetTaskFailed() != nil ||
		e.GetChildWorkflowInstanceCompleted() != nil || e.GetChildWorkflowInstanceFailed() != nil
	if isCompletion && state.HasTamperMarker() {
		log.Debugf("Workflow actor '%s': dropping completion event for tombstoned workflow", o.actorID)
		return api.ErrInstanceNotFound
	}

	// ackDropped acknowledges a child completion this workflow will never
	// consume, after confirming the cache it was judged on is current: the
	// child clears its pending notification on this ack.
	ackDropped := func(reason string) error {
		if err := o.confirmCachedState(ctx, state); err != nil {
			return err
		}
		log.Debugf("Workflow actor '%s': dropping child completion from '%s': %s", o.actorID, sender.instanceID, reason)
		return nil
	}

	// A completed parent can never consume a child completion. Ack it here
	// rather than queueing a turn: the terminal path would re-issue the
	// recursive terminate and the child would re-send.
	if runtimestate.IsCompleted(o.rstate) && (e.GetChildWorkflowInstanceCompleted() != nil || e.GetChildWorkflowInstanceFailed() != nil) {
		return ackDropped("the workflow has completed")
	}

	// Only reject user events when the workflow is stalled.
	if o.rstate.Stalled != nil && e.GetEventRaised() != nil {
		return api.ErrStalled
	}

	// A child re-sends its completion on stray fires and after failures, and
	// task ids restart on ContinueAsNew: a completion for task N from any
	// instance other than the child this generation created for N is a
	// straggler from a previous generation and is acked without effect.
	if sender.instanceID != "" {
		if created := childCreatedFor(state.History, e); created != nil && created.GetInstanceId() != sender.instanceID {
			return ackDropped("the task's current child is '" + created.GetInstanceId() + "'")
		}
	}
	if sender.parentExecutionID != "" {
		if cur := o.getExecutionStartedEvent(state).GetWorkflowInstance().GetExecutionId().GetValue(); cur != "" && cur != sender.parentExecutionID {
			return ackDropped("it was created under a previous execution")
		}
	}

	// Drop completion events whose resolution is already in history or the
	// inbox; otherwise an inbox redelivery (e.g. an activity actor reminder
	// firing twice during pod migration) would pin the workflow in a replay/spin
	// loop.
	if dedup.IsDuplicateCompletion(e, state.History, state.Inbox) {
		log.Debugf("Workflow actor '%s': dropping duplicate completion event already present in history/inbox; re-driving the wake-up so the inbox row is not stranded", o.actorID)
		return o.driveNewEvent(ctx, e, state)
	}

	// Drop redelivered external events the same way: a RaiseEvent re-sent to
	// this actor (e.g. an AddWorkflowEvent invocation retried under placement
	// churn) keeps the same ingestion timestamp, so it matches an EventRaised
	// already in history or the inbox by (event name, ingestion timestamp).
	// duplicate the event in history; instead re-assert the wake-up reminder so
	// a still-pending inbox row that lost its reminder gets re-driven. Distinct
	// RaiseEvents are guaranteed distinct timestamps by the backend at ingestion
	// (Actors.uniqueEventTimestamp), so they fall through to be appended
	// normally even when raced onto the same wall-clock nanosecond.
	if dedup.IsDuplicateExternalEvent(e, state.History, state.Inbox) {
		log.Debugf("Workflow actor '%s': dropping duplicate external event already present in history/inbox; re-driving the wake-up so the inbox row is not stranded", o.actorID)
		return o.driveNewEvent(ctx, e, state)
	}

	if e.GetTaskCompleted() != nil || e.GetTaskFailed() != nil {
		o.activityResultAwaited.CompareAndSwap(true, false)
	}

	if err := o.verifyAndAbsorbAttestation(ctx, state, e); err != nil {
		return err
	}

	// Save the inbox event BEFORE arming its wake-up (durable reminder or
	// local drive; see driveNewEvent). Under the WorkflowsFastPath
	// preview the recovery chain after this save is: local drive; on drive
	// failure, escalation to the durable per-event reminder; and behind
	// both, the per-instance janitor reminder (<= 1 period). The
	// reminder's dueTime is anchored at the workflow's start timestamp
	// (state.History[0].Timestamp), which is in the past, so the scheduler
	// fires it immediately on Create. Under placement rebalance the firing
	// daprd may not be the host that ran AddWorkflowEvent: it loads the
	// store, sees no inbox event, acks SUCCESS, and the scheduler deletes
	// the reminder. By the time the save eventually commits the inbox row
	// is stranded with no driver, and the activity actor's publishResult
	// already returned nil so its retry-forever 'run-activity' reminder
	// no longer fires. The workflow freezes in RUNNING.
	//
	// Saving first inverts the failure mode into something the existing
	// recovery paths already handle: if signAndSaveState succeeds but the
	// reminder Create then crashes / times out, the activity actor's
	// publishResult sees the RPC error and its retry-forever reminder
	// re-fires, the next AddWorkflowEvent hits dedup.IsDuplicateCompletion
	// (the row is already in inbox), and the dedup branch above calls
	// assertNewEventReminder which deterministically re-creates the
	// reminder. The inbox is never stranded.
	//
	// The reminder must target the local actor (o.appID), not the router's
	// source app. For cross-app events (e.g. ExecutionTerminated from a
	// parent in another app), router.SourceAppID is the sender's app and
	// would route the reminder to a non-existent remote actor.
	log.Debugf("Workflow actor '%s': adding event to the workflow inbox", o.actorID)
	state.AddToInbox(e)
	if err := o.signAndSaveState(ctx, state); err != nil {
		return err
	}

	if err := o.driveNewEvent(ctx, e, state); err != nil {
		return err
	}

	return nil
}

// verifyAndAbsorbAttestation verifies any attestation on the incoming event
// against the signed history and Sentry trust anchors, absorbs the signer
// certificate into the ext-sigcert table, and strips it from the event.
// Unmatched completions are dropped; genuine verification failures tombstone
// the workflow. Both return ErrInstanceNotFound so the sender stops
// re-delivering. No-op when signing is disabled; locally-authored synthetic
// failures are exempt (no attestation by design).
func (o *orchestrator) verifyAndAbsorbAttestation(ctx context.Context, state *wfenginestate.State, e *backend.HistoryEvent) error {
	if o.isLocalSyntheticFailure(e) {
		return nil
	}
	verr := o.signing.VerifyInboxAttestation(ctx, state, e)
	if verr == nil {
		return nil
	}

	// Reclassify against durable truth before acting: a stale cache can make
	// a legitimate completion look tampered or unmatched, the unknown-id drop
	// below is terminal for the sender, and tombstoning is permanent. Load
	// failures are retryable; the fresh verdict and state drive the decision.
	// Verify a clone so nothing is observably mutated.
	opts := wfenginestate.Options{
		AppID:             o.appID,
		Namespace:         o.namespace,
		WorkflowActorType: o.actorType,
		ActivityActorType: o.activityActorType,
		Signer:            o.signer,
	}
	fresh, lerr := wfenginestate.LoadWorkflowState(ctx, o.actorState, o.actorID, opts)
	if lerr != nil {
		// A verification failure from the durable load is independent
		// confirmation of tampering, not a transient condition: tombstone
		// rather than retry forever.
		var verifyErr *staterrors.VerificationError
		if errors.As(lerr, &verifyErr) {
			log.Warnf("Workflow actor '%s': durable state failed verification while classifying an attestation failure, tombstoning workflow: %s", o.actorID, lerr)
			condemned := fresh
			if condemned == nil {
				condemned = state
			}
			if _, _, terr := o.tombstoneTamperedState(ctx, opts, condemned, lerr); terr != nil {
				return terr
			}
			return api.ErrInstanceNotFound
		}
		return wferrors.NewRecoverable(fmt.Errorf("failed to reload state to classify attestation failure (%s): %w", verr, lerr))
	}
	if fresh == nil {
		// Purged since the cached load: nothing to protect.
		return api.ErrInstanceNotFound
	}
	clone, _ := proto.Clone(e).(*backend.HistoryEvent)
	if clone == nil {
		return wferrors.NewRecoverable(errors.New("failed to clone event to classify attestation failure"))
	}
	fverr := o.signing.VerifyInboxAttestation(ctx, fresh, clone)
	if fverr == nil {
		log.Warnf("Workflow actor '%s': attestation verification failed against cached state but passed against durable state; refreshing cache and asking the sender to retry: %s", o.actorID, verr)
		o.invalidateCachedState()
		return verr
	}

	// Not tampering: ContinueAsNew resets history and a rolled-back save can
	// retract a scheduling row, so drop the unmatched completion like the
	// unsigned path does (stripUnmatchedResolutions). Nothing is persisted,
	// so a forged completion gains an attacker nothing.
	if errors.Is(fverr, signing.ErrUnknownTaskScheduledID) {
		log.Warnf("Workflow actor '%s': dropping completion with no matching scheduled task in signed history: %s", o.actorID, fverr)
		return api.ErrInstanceNotFound
	}

	log.Warnf("Workflow actor '%s': attestation verification failed, tombstoning workflow: %s", o.actorID, fverr)
	if _, _, terr := o.tombstoneTamperedState(ctx, opts, fresh, fverr); terr != nil {
		return terr
	}
	return api.ErrInstanceNotFound
}

// childCreatedFor returns the ChildWorkflowInstanceCreated event this
// history holds for the task a child completion resolves, or nil.
func childCreatedFor(history []*backend.HistoryEvent, e *backend.HistoryEvent) *protos.ChildWorkflowInstanceCreatedEvent {
	var taskID int32
	switch {
	case e.GetChildWorkflowInstanceCompleted() != nil:
		taskID = e.GetChildWorkflowInstanceCompleted().GetTaskScheduledId()
	case e.GetChildWorkflowInstanceFailed() != nil:
		taskID = e.GetChildWorkflowInstanceFailed().GetTaskScheduledId()
	default:
		return nil
	}
	for _, h := range history {
		if c := h.GetChildWorkflowInstanceCreated(); c != nil && h.GetEventId() == taskID {
			return c
		}
	}
	return nil
}

// senderFromMetadata extracts the delivering child's identity from request
// metadata; zero for senders that do not carry it.
func senderFromMetadata(md map[string]*internalsv1pb.ListStringValue) completionSender {
	first := func(key string) string {
		if v, ok := md[key]; ok && len(v.GetValues()) > 0 {
			return v.GetValues()[0]
		}
		return ""
	}
	return completionSender{
		instanceID:        first(todo.MetadataSenderInstanceID),
		parentExecutionID: first(todo.MetadataParentExecutionID),
	}
}
