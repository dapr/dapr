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
	grpcstatus "google.golang.org/grpc/status"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

// dispatchRetryBackoff is the initial firing delay of the safety reminder.
// FailurePolicy on the reminder retries at 1s after that until the handler
// returns nil.
const dispatchRetryBackoff = time.Second

// createDispatchRetryReminder schedules the per-workflow safety reminder
// that re-attempts dispatch for any TaskScheduled whose result has not yet
// arrived. The reminder name is deterministic so concurrent failures from
// the same workflow collapse onto a single scheduler entry.
func (o *orchestrator) createDispatchRetryReminder(ctx context.Context) error {
	return o.createWorkflowReminder(ctx, reminderNameDispatchRetry, nil, time.Now().Add(dispatchRetryBackoff), o.appID, nil)
}

// deleteDispatchRetryReminder removes the safety reminder. NotFound is
// treated as already-deleted (idempotent on concurrent fires).
func (o *orchestrator) deleteDispatchRetryReminder(ctx context.Context) error {
	actorType := o.actorTypeBuilder.Workflow(o.appID)
	err := o.reminders.Delete(ctx, &actorapi.DeleteReminderRequest{
		Name:      reminderNameDispatchRetry,
		ActorType: actorType,
		ActorID:   o.actorID,
	})
	if err == nil {
		return nil
	}
	if s, ok := grpcstatus.FromError(err); ok && s.Code() == codes.NotFound {
		return nil
	}
	return fmt.Errorf("workflow actor '%s': failed to delete dispatch-retry reminder: %w", o.actorID, err)
}

// redispatchStranded re-attempts dispatch for every TaskScheduled in
// state.History whose completion is not yet present in either history or
// inbox. The retry is idempotent: the activity actor's run-activity
// reminder is upserted under a fixed name, so re-dispatching an activity
// that is currently in flight is a no-op.
//
// Returns nil (and deletes the reminder) when there is nothing left to
// retry. Returns a recoverable error (leaving the reminder to fire again
// via its FailurePolicy) when at least one dispatch is still failing.
func (o *orchestrator) redispatchStranded(ctx context.Context, _ *actorapi.Reminder) error {
	state, _, err := o.loadInternalState(ctx)
	if err != nil {
		return wferrors.NewRecoverable(fmt.Errorf("dispatch-retry: load state: %w", err))
	}
	if state == nil {
		// Workflow has been purged; safety reminder is stale.
		return o.deleteDispatchRetryReminder(ctx)
	}

	// Build the set of resolved TaskScheduled IDs from history and inbox.
	// An ID is resolved if there is a TaskCompleted or TaskFailed event
	// for it anywhere in either slice.
	resolved := make(map[int32]struct{})
	collectResolved(resolved, state.History)
	collectResolved(resolved, state.Inbox)

	// Collect unresolved TaskScheduled events in history order.
	var unresolved []*backend.HistoryEvent
	for _, e := range state.History {
		ts := e.GetTaskScheduled()
		if ts == nil {
			continue
		}
		// Cross-app TaskScheduled retry is intentionally not handled here
		// in this PR; see PR description for the rationale. Skip them so
		// the safety reminder doesn't accidentally re-dispatch cross-app
		// activities without propagated history.
		if router := e.GetRouter(); router != nil && router.TargetAppID != nil {
			continue
		}
		if _, ok := resolved[e.GetEventId()]; ok {
			continue
		}
		unresolved = append(unresolved, e)
	}

	if len(unresolved) == 0 {
		// Everything has been resolved (or the workflow is past these
		// tasks); the safety reminder has done its job.
		return o.deleteDispatchRetryReminder(ctx)
	}

	workflowName := o.getExecutionStartedEvent(state).GetName()
	dueTime := time.Now()
	if len(state.History) > 0 {
		dueTime = state.History[0].GetTimestamp().AsTime()
	}

	var dispatchErrs []error
	for _, e := range unresolved {
		// ph is nil: the safety reminder does not carry propagated
		// history. Local activities don't need it; cross-app TaskScheduled
		// are filtered out above.
		derr := o.callActivity(ctx, e, dueTime, state.Generation, nil, workflowName)
		switch {
		case derr == nil:
			// Dispatch succeeded; the activity actor's run-activity
			// reminder now owns delivery.
		case errors.Is(derr, todo.ErrDuplicateInvocation):
			// Activity actor already has an in-flight run-activity
			// reminder for this task; nothing to do.
		default:
			dispatchErrs = append(dispatchErrs, fmt.Errorf("redispatch of TaskScheduled %d: %w", e.GetEventId(), derr))
		}
	}

	if len(dispatchErrs) > 0 {
		// Leave the reminder in place so its FailurePolicy fires again.
		return wferrors.NewRecoverable(errors.Join(dispatchErrs...))
	}

	// All unresolved tasks have been (re-)dispatched. The reminder's job
	// from this side is done; the activity actors' own run-activity
	// reminders will deliver results back to the workflow inbox.
	return o.deleteDispatchRetryReminder(ctx)
}

// collectResolved scans events for TaskCompleted/TaskFailed and records
// their TaskScheduledId in the resolved set.
func collectResolved(resolved map[int32]struct{}, events []*backend.HistoryEvent) {
	for _, e := range events {
		switch t := e.GetEventType().(type) {
		case *protos.HistoryEvent_TaskCompleted:
			resolved[t.TaskCompleted.GetTaskScheduledId()] = struct{}{}
		case *protos.HistoryEvent_TaskFailed:
			resolved[t.TaskFailed.GetTaskScheduledId()] = struct{}{}
		}
	}
}
