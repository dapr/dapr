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

	"google.golang.org/protobuf/proto"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/dedup"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
)

const (
	reminderPrefixStart    = "start"
	reminderPrefixNewEvent = "new-event"
	reminderPrefixTimer    = "timer-"
)

func (o *orchestrator) addWorkflowEvent(ctx context.Context, historyEventBytes []byte) error {
	state, _, err := o.loadInternalState(ctx)
	if err != nil {
		return err
	}

	if state == nil {
		log.Errorf("Workflow actor '%s': cannot add event to workflow as state has been purged. Ignoring event.", o.actorID)
		return api.ErrInstanceNotFound
	}

	var e backend.HistoryEvent
	err = proto.Unmarshal(historyEventBytes, &e)
	if err != nil {
		return err
	}

	// Only reject user events when the workflow is stalled.
	if o.rstate.Stalled != nil && e.GetEventRaised() != nil {
		return api.ErrStalled
	}

	// Drop completion events whose resolution is already in history or the
	// inbox; otherwise an inbox redelivery (e.g. an activity actor reminder
	// firing twice during pod migration) would pin the workflow in a replay/spin
	// loop.
	if dedup.IsDuplicateCompletion(&e, state.History, state.Inbox) {
		log.Debugf("Workflow actor '%s': dropping duplicate completion event already present in history/inbox; re-asserting wake-up reminder so the inbox row is not stranded", o.actorID)
		return o.assertNewEventReminder(ctx, &e, state)
	}

	if e.GetTaskCompleted() != nil || e.GetTaskFailed() != nil {
		o.activityResultAwaited.CompareAndSwap(true, false)
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
	if err := o.assertNewEventReminder(ctx, &e, state); err != nil {
		return err
	}

	log.Debugf("Workflow actor '%s': adding event to the workflow inbox", o.actorID)
	state.AddToInbox(&e)
	if err := o.saveInternalState(ctx, state); err != nil {
		return err
	}

	return nil
}
