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

// admitOutcome is the completion-admission decision for an inbound event.
type admitOutcome uint8

const (
	admitDrop      admitOutcome = iota // ack without effect, or reject with err (zero value)
	admitDuplicate                     // already recorded: re-drive the wake-up only
	admitInbox                         // persist to the durable inbox, then drive
	admitFold                          // hold in memory for the next turn's commit
)

// admission is classifyEvent's verdict, applied by admitEvent.
type admission struct {
	outcome admitOutcome
	reason  string     // acked drop: why the child completion is never consumed
	err     error      // rejected drop: returned to the sender instead of an ack
	pending *foldEntry // duplicate of a held completion: the entry a retry joins
}

// classifyEvent decides how e is admitted against the loaded state. It does
// no I/O and mutates nothing. canFold is the WorkflowsFastPath
// AddWorkflowEvent entry and alone may yield admitFold.
func (o *orchestrator) classifyEvent(e *backend.HistoryEvent, state *wfenginestate.State, sender completionSender, canFold bool) admission {
	if state == nil {
		log.Errorf("Workflow actor '%s': cannot add event to workflow as state has been purged. Ignoring event.", o.actorID)
		return admission{err: api.ErrInstanceNotFound}
	}

	isActivity := e.GetTaskCompleted() != nil || e.GetTaskFailed() != nil
	isChild := e.GetChildWorkflowInstanceCompleted() != nil || e.GetChildWorkflowInstanceFailed() != nil

	// A tombstoned workflow (unsigned tamper marker at the end of history)
	// rejects completions with ErrInstanceNotFound, which the activity actor
	// and the child's completion dispatch treat as terminal, so senders stop
	// re-delivering to a workflow that will never accept the result. Other
	// event types (RaiseEvent, terminate, etc.) still flow through.
	if (isActivity || isChild) && state.HasTamperMarker() {
		log.Debugf("Workflow actor '%s': dropping completion event for tombstoned workflow", o.actorID)
		return admission{err: api.ErrInstanceNotFound}
	}

	// A completed parent can never consume a child completion. Ack it here
	// rather than queueing a turn: the terminal path would re-issue the
	// recursive terminate and the child would re-send.
	if isChild && runtimestate.IsCompleted(o.rstate) {
		return admission{reason: "the workflow has completed"}
	}

	// Only reject user events when the workflow is stalled.
	if o.rstate.Stalled != nil && e.GetEventRaised() != nil {
		return admission{err: api.ErrStalled}
	}

	// A child re-sends its completion on stray fires and after failures, and
	// task ids restart on ContinueAsNew: a completion for task N from any
	// instance other than the child this generation created for N is a
	// straggler from a previous generation and is acked without effect.
	if sender.instanceID != "" {
		if created := childCreatedFor(state.History, e); created != nil && created.GetInstanceId() != sender.instanceID {
			return admission{reason: "the task's current child is '" + created.GetInstanceId() + "'"}
		}
	}
	if sender.parentExecutionID != "" {
		if cur := o.getExecutionStartedEvent(state).GetWorkflowInstance().GetExecutionId().GetValue(); cur != "" && cur != sender.parentExecutionID {
			return admission{reason: "it was created under a previous execution"}
		}
	}

	// Fold only sender-retried ACTIVITY completions against a healthy,
	// running instance. Child completions must not fold: the child publishes
	// under its own turn lock, which can deadlock against a parent turn
	// dispatching back into it. An empty history never scheduled an activity,
	// and a held entry would pin its sender against a state only the
	// unstartable classification can settle. A TaskExecutionId mismatch is a
	// straggler from a previous execution (ids reset on ContinueAsNew).
	hold := canFold && isActivity && o.rstate.GetStalled() == nil && len(state.History) > 0
	if hold && !o.foldExecutionMatches(e, state) {
		log.Debugf("Workflow actor '%s': completion's task execution id does not match current history; taking the durable inbox path", o.actorID)
		hold = false
	}

	// Drop completion events whose resolution is already in history or the
	// inbox; otherwise an inbox redelivery (e.g. an activity actor reminder
	// firing twice during pod migration) would pin the workflow in a replay/spin
	// loop.
	if dedup.IsDuplicateCompletion(e, state.History, state.Inbox) {
		log.Debugf("Workflow actor '%s': dropping duplicate completion already in history/inbox; re-driving the wake-up", o.actorID)
		return admission{outcome: admitDuplicate}
	}

	// A redelivered RaiseEvent (e.g. an AddWorkflowEvent retried under
	// placement churn) keeps its ingestion timestamp, so it matches by (name,
	// timestamp). Distinct RaiseEvents get distinct timestamps at ingestion
	// (Actors.uniqueEventTimestamp) even when raced onto the same nanosecond.
	if dedup.IsDuplicateExternalEvent(e, state.History, state.Inbox) {
		log.Debugf("Workflow actor '%s': dropping duplicate external event already present in history/inbox; re-driving the wake-up so the inbox row is not stranded", o.actorID)
		return admission{outcome: admitDuplicate}
	}

	if !hold {
		return admission{outcome: admitInbox}
	}

	// A retry of a completion still only held in memory must NOT be acked
	// yet: the retry chain is the durability until the folding turn commits,
	// so it joins the pending entry's resolution.
	if pending := o.foldPendingEntry(e); pending != nil {
		log.Debugf("Workflow actor '%s': joining retry to the pending fold entry; re-driving the wake-up", o.actorID)
		return admission{outcome: admitDuplicate, pending: pending}
	}
	return admission{outcome: admitFold}
}

// admitEvent runs the completion-admission decision for an inbound event and
// applies its outcome. With canFold (the WorkflowsFastPath AddWorkflowEvent
// entry) a held event's entry is returned for the caller to wait on after
// releasing the actor lock; nil, nil means the outcome completed inline.
func (o *orchestrator) admitEvent(ctx context.Context, e *backend.HistoryEvent, sender completionSender, canFold bool) (*foldEntry, error) {
	fresh := o.state == nil
	state, _, err := o.loadInternalState(ctx)
	if err != nil {
		return nil, err
	}

	a := o.classifyEvent(e, state, sender, canFold)
	if a.err != nil {
		return nil, a.err
	}
	if a.reason != "" {
		// Acknowledge a child completion this workflow will never consume
		// only after confirming the cache it was judged on is current: the
		// child clears its pending notification on this ack.
		if !fresh {
			if err := o.confirmCachedState(ctx, state); err != nil {
				return nil, err
			}
		}
		log.Debugf("Workflow actor '%s': dropping child completion from '%s': %s", o.actorID, sender.instanceID, a.reason)
		return nil, nil
	}
	if a.outcome == admitDuplicate {
		if err := o.driveNewEvent(ctx, e, state); err != nil {
			return nil, err
		}
		return a.pending, nil
	}

	if e.GetTaskCompleted() != nil || e.GetTaskFailed() != nil {
		o.activityResultAwaited.CompareAndSwap(true, false)
	}

	// Absorbs the signer cert into state; the inbox save or the folding
	// turn's commit persists it alongside the event.
	if err := o.verifyAndAbsorbAttestation(ctx, state, e); err != nil {
		return nil, err
	}

	if a.outcome == admitFold {
		return o.foldSubmit(ctx, e, state), nil
	}

	// Save the inbox event BEFORE arming its wake-up (see driveNewEvent).
	// The wake-up is due in the past, so it fires immediately; under
	// placement rebalance another host may fire it, see no inbox row, ack
	// SUCCESS and lose the reminder, while the sender already saw nil and
	// stopped retrying: the row would commit with no driver. Saving first
	// makes a failed arm recoverable instead: the sender sees the error and
	// re-delivers, which classifies as admitDuplicate and re-creates the
	// wake-up by deterministic name. The wake-up targets the local actor
	// (o.appID), never router.SourceAppID, which for cross-app events is
	// the sender's app.
	log.Debugf("Workflow actor '%s': adding event to the workflow inbox", o.actorID)
	state.AddToInbox(e)
	if err := o.signAndSaveState(ctx, state); err != nil {
		return nil, err
	}
	return nil, o.driveNewEvent(ctx, e, state)
}

// addWorkflowEvent admits an inbound event on the durable inbox path.
func (o *orchestrator) addWorkflowEvent(ctx context.Context, e *backend.HistoryEvent, sender completionSender) error {
	_, err := o.admitEvent(ctx, e, sender, false)
	return err
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
	opts := o.stateOptions()
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
