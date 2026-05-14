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

	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/dedup"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/events"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
)

const (
	reminderPrefixStart    = "start"
	reminderPrefixNewEvent = "new-event"
	reminderPrefixTimer    = "timer-"
)

func (o *orchestrator) addWorkflowEvent(ctx context.Context, e *backend.HistoryEvent) error {
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
	// completion events with ErrInstanceNotFound. The activity actor
	// treats ErrInstanceNotFound as terminal and stops re-delivering, so
	// we don't loop the parent's actor lock against a workflow that will
	// never accept the result. Other event types (RaiseEvent, terminate,
	// etc.) still flow through.
	isCompletion := e.GetTaskCompleted() != nil || e.GetTaskFailed() != nil ||
		e.GetChildWorkflowInstanceCompleted() != nil || e.GetChildWorkflowInstanceFailed() != nil
	if isCompletion && state.HasTamperMarker() {
		log.Debugf("Workflow actor '%s': dropping completion event for tombstoned workflow", o.actorID)
		return api.ErrInstanceNotFound
	}

	// Only reject user events when the workflow is stalled.
	if o.rstate.Stalled != nil && e.GetEventRaised() != nil {
		return api.ErrStalled
	}

	// Drop completion events whose resolution is already in history or the
	// inbox; otherwise an inbox redelivery (e.g. an activity actor reminder
	// firing twice during pod migration) would pin the workflow in a
	// replay/spin loop.
	if dedup.IsDuplicateCompletion(e, state.History, state.Inbox) {
		log.Debugf("Workflow actor '%s': dropping duplicate completion event already present in history/inbox", o.actorID)
		return nil
	}

	if e.GetTaskCompleted() != nil || e.GetTaskFailed() != nil {
		o.activityResultAwaited.CompareAndSwap(true, false)
	}

	// Verify any attestation on the incoming event against the signed history
	// and Sentry trust anchors, then absorb the signer certificate into the
	// ext-sigcert table and strip the companion cert from the event so the
	// stored form is cert-free. On any verification failure the workflow is
	// tombstoned. No-op when signing is disabled.
	if verr := o.signing.VerifyInboxAttestation(ctx, state, e); verr != nil {
		log.Warnf("Workflow actor '%s': attestation verification failed, tombstoning workflow: %s", o.actorID, verr)
		opts := wfenginestate.Options{
			AppID:             o.appID,
			Namespace:         o.namespace,
			WorkflowActorType: o.actorType,
			ActivityActorType: o.activityActorType,
			Signer:            o.signer,
		}
		if _, _, terr := o.tombstoneTamperedState(ctx, opts, state, verr); terr != nil {
			return terr
		}
		// Return ErrInstanceNotFound rather than the verification
		// error so the activity actor on the sender side recognizes
		// the workflow as gone and stops re-executing the activity.
		// The reason for tombstoning is preserved in the workflow's
		// FailureDetails (errorType=DAPR_WORKFLOW_HISTORY_TAMPERED,
		// errorMessage=verr.Error()) for callers polling metadata.
		return api.ErrInstanceNotFound
	}

	// Create the wake-up reminder BEFORE mutating any state. If reminder
	// creation fails we return without touching the in-memory inbox, so a
	// retry sees clean state and dedup stays exact. If we instead saved
	// first and then failed to create the reminder, the event would be
	// durable but un-driven; if we mutated the in-memory inbox before save
	// and then failed, a retry would see the orphan event in the cached
	// inbox, dedup would drop it, and cron would auto-delete the reminder.
	//
	// The reminder must target the local actor (o.appID), not the router's
	// source app. For cross-app events (e.g. ExecutionTerminated from a
	// parent in another app), router.SourceAppID is the sender's app and
	// would route the reminder to a non-existent remote actor.
	sourceAppID := o.appID
	dueTime := e.Timestamp.AsTime()
	if len(state.History) > 0 {
		dueTime = state.History[0].Timestamp.AsTime()
	}
	wfName := o.getExecutionStartedEvent(state).GetName()
	reminderName := events.EventReminderName(reminderPrefixNewEvent, e)

	if err := o.createWorkflowReminder(ctx, reminderName, nil, dueTime, sourceAppID, &wfName); err != nil {
		return err
	}

	log.Debugf("Workflow actor '%s': adding event to the workflow inbox", o.actorID)
	state.AddToInbox(e)
	if err := o.signAndSaveState(ctx, state); err != nil {
		return err
	}

	return nil
}
