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

package ordering

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common/pendingstart"
	keyactor "github.com/dapr/dapr/pkg/actors/targets/workflow/ordering"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/kit/ptr"
)

// Gate decides the turn of an instance woken by reminder: run it, when the
// instance is not waiting for its key or this wake-up is its release, which
// is recorded in state; end it, when the instance is still queued; or
// complete the queued instance as terminated by term without running it.
func Gate(reminder *actorapi.Reminder, state *wfenginestate.State) (run bool, term *backend.HistoryEvent) {
	if !state.OrderingGated() {
		return true, nil
	}
	if term = terminateEvent(state.Inbox); term != nil {
		return false, term
	}
	_, cancel, ok := keyactor.ParseReminderName(reminder.Name)
	switch {
	case !ok:
		return false, nil
	case cancel:
		return false, cancelEvent(reminder)
	}
	state.Ordering.Released = ptr.Of(true)
	return true, nil
}

func IsRelease(reminderName string) bool {
	_, _, ok := keyactor.ParseReminderName(reminderName)
	return ok
}

func terminateEvent(inbox []*backend.HistoryEvent) *backend.HistoryEvent {
	for _, e := range inbox {
		if e.GetExecutionTerminated() != nil {
			return e
		}
	}
	return nil
}

// cancelEvent is the termination of an instance its policy cancels, naming
// the failure it was cancelled past.
func cancelEvent(reminder *actorapi.Reminder) *backend.HistoryEvent {
	rd := reminderData(reminder)
	reason := fmt.Sprintf("cancelled by its ordering key %q: the workflow before it did not complete successfully", rd.Key)
	if rd.After != "" {
		reason = fmt.Sprintf("cancelled by its ordering key %q: workflow %q before it did not complete successfully", rd.Key, rd.After)
	}
	out, _ := json.Marshal(reason)
	return &backend.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionTerminated{ExecutionTerminated: &protos.ExecutionTerminatedEvent{
			Input: wrapperspb.String(string(out)),
		}},
	}
}

func reminderData(reminder *actorapi.Reminder) keyactor.ReminderData {
	var rd keyactor.ReminderData
	_ = json.Unmarshal(reminder.Data.GetValue(), &rd)
	return rd
}

// TerminateQueued completes a queued instance as terminated by term without
// handing it to an executor, so none of its code runs whatever SDK hosts it.
func (o *Ordering) TerminateQueued(ctx context.Context, state *wfenginestate.State, term *backend.HistoryEvent) error {
	state.AddToHistory(pendingstart.Event(state))
	state.AddToHistory(term)
	state.AddToHistory(&backend.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionCompleted{ExecutionCompleted: &protos.ExecutionCompletedEvent{
			WorkflowStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_TERMINATED,
			Result:         term.GetExecutionTerminated().GetInput(),
		}},
	})
	state.ClearInbox()
	return o.Save(ctx, state)
}

// Release reports whether a release or cancel reminder is for the run the
// actor holds and that still owes the key its turn: a queued one is released
// or cancelled by the gate, a finished one settles. A reminder for another
// run, or for this run after the key acknowledged that it is done, tells the
// key to drop that entry if it still lists it, as after a late enqueue: the
// run is skipped, or purged when the actor holds no state at all.
func (o *Ordering) Release(ctx context.Context, reminder *actorapi.Reminder, state *wfenginestate.State) (bool, error) {
	exec, _, _ := keyactor.ParseReminderName(reminder.Name)
	if state != nil && state.Ordering.GetCreateExecutionId() == exec && !state.Ordering.GetDone() {
		return true, nil
	}
	key := reminderData(reminder).Key
	if key == "" {
		log.Warnf("Workflow actor '%s': dropping ordering reminder '%s' with no key", o.ActorID, reminder.Name)
		return false, nil
	}
	outcome := keyactor.OutcomeSkipped
	if state == nil {
		outcome = keyactor.OutcomePurged
	}
	return false, o.done(ctx, key, keyactor.Entry{InstanceID: o.ActorID, CreateExecutionID: exec}, outcome)
}
