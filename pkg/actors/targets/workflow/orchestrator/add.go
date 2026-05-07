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
	// firing twice during pod migration) would pin the workflow in a
	// replay/spin loop.
	if dedup.IsDuplicateCompletion(&e, state.History, state.Inbox) {
		log.Debugf("Workflow actor '%s': dropping duplicate completion event already present in history/inbox", o.actorID)
		return nil
	}

	if e.GetTaskCompleted() != nil || e.GetTaskFailed() != nil {
		o.activityResultAwaited.CompareAndSwap(true, false)
	}
	log.Debugf("Workflow actor '%s': adding event to the workflow inbox", o.actorID)
	state.AddToInbox(&e)

	if err := o.saveInternalState(ctx, state); err != nil {
		return err
	}

	// For activity completion events, we want to create the reminder on the same app where this workflow actor is
	// hosted, so use the source app from the router.
	// For sub-orchestrator completion events we want to create the reminder on the current app.
	sourceAppID := o.appID
	returningToParent := e.GetSubOrchestrationInstanceCompleted() != nil || e.GetSubOrchestrationInstanceFailed() != nil
	if !returningToParent && e.GetRouter() != nil {
		sourceAppID = e.GetRouter().GetSourceAppID()
	}

	dueTime := e.Timestamp.AsTime()
	if len(state.History) > 0 {
		dueTime = state.History[0].Timestamp.AsTime()
	}
	if _, err := o.createWorkflowReminder(ctx, reminderPrefixNewEvent, nil, dueTime, sourceAppID); err != nil {
		return err
	}

	return nil
}
