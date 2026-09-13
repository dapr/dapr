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
	"time"

	"github.com/cenkalti/backoff/v4"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/reminders"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common/pendingstart"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/events"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/backend"
)

// stillNeeded reports whether the durable state still needs the reminder a
// detached arm is retrying for.
type stillNeeded func(*wfenginestate.State) bool

// startStillPending: the same pending start (by reminder name) is still in
// the inbox with an empty history.
func startStillPending(reminderName string) stillNeeded {
	return func(state *wfenginestate.State) bool {
		pending := pendingstart.Event(state)
		return pending != nil && events.EventReminderName(reminderPrefixStart, pending) == reminderName
	}
}

// inboxPending: a non-terminal instance still holds inbox rows to drive.
func inboxPending(state *wfenginestate.State) bool {
	return state != nil && len(state.Inbox) > 0 && !state.IsCompleted()
}

// armDetachedOnCreateError hands a wake-up reminder create that failed after
// its inbox row was committed to a detached retry, unless the error is a
// permanent request error. The invocation context is the placement claim
// context, which a dissemination round cancels regardless of the Scheduler's
// health, so a context error must not abandon the row's only driver.
func (o *orchestrator) armDetachedOnCreateError(reminderName string, start time.Time, wfName string, needed stillNeeded, err error) {
	ctxErr := errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
	if !ctxErr && common.IsPermanentCreateError(err) {
		return
	}
	log.Warnf("Workflow actor '%s': failed to create reminder '%s' after its inbox row was committed, retrying detached from the invocation: %v", o.actorID, reminderName, err)
	o.armReminderDetached(reminderName, start, wfName, needed, "")
}

// armReminderDetached registers the deterministic wake-up reminder on the
// detached runner, retrying while the durable state still needs it. Arms for
// the same reminder collapse onto the one in flight, across residencies.
// armedStatus, when set, is recorded once the reminder is actually created.
func (o *orchestrator) armReminderDetached(reminderName string, start time.Time, wfName string, needed stillNeeded, armedStatus string) {
	key := o.actorType + "||" + o.actorID + "||" + reminderName
	started, inflight := o.detached.GoKeyed(key, func(ctx context.Context) {
		o.armReminder(ctx, reminderName, start, wfName, needed, armedStatus)
	})
	switch {
	case started:
		diag.DefaultWorkflowMonitoring.WorkflowLocalWake(context.Background(), diag.StatusArmDetached)
	case !inflight:
		log.Debugf("Workflow actor '%s': not arming reminder '%s', the runtime is shutting down", o.actorID, reminderName)
		diag.DefaultWorkflowMonitoring.WorkflowLocalWake(context.Background(), diag.StatusArmDetachedSkipped)
	}
}

// armReminder is the body of a detached arm. Before every attempt after the
// first it re-reads the durable state and stops once the row no longer needs
// the reminder, so a stale arm cannot register a reminder against a driven,
// purged or recreated instance; it then skips the attempt when the reminder
// is already registered (another host or a create retry armed it). Attempts
// are single creates with exponential backoff: ResourceExhausted and the
// actor type not being hosted (an app reconnect gap) are retried here, unlike
// in the bounded create, because the row is committed and both clear.
func (o *orchestrator) armReminder(ctx context.Context, reminderName string, start time.Time, wfName string, needed stillNeeded, armedStatus string) {
	actorType := o.actorTypeBuilder.Workflow(o.appID)
	req, err := o.buildReminderRequest(reminderName, nil, start, actorType, &wfName)
	if err != nil {
		log.Errorf("Workflow actor '%s': detached create of reminder '%s' gave up: %v", o.actorID, reminderName, err)
		diag.DefaultWorkflowMonitoring.WorkflowLocalWake(context.Background(), diag.StatusArmDetachedFailed)
		return
	}

	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = 100 * time.Millisecond
	bo.MaxInterval = 5 * time.Second
	bo.MaxElapsedTime = 0

	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			state, lerr := wfenginestate.LoadWorkflowState(ctx, o.actorState, o.actorID, o.stateOptions())
			if lerr == nil && !needed(state) {
				log.Debugf("Workflow actor '%s': reminder '%s' is no longer needed, detached arm stopped", o.actorID, reminderName)
				return
			}
		}

		rem, gerr := o.reminders.Get(ctx, &actorapi.GetReminderRequest{Name: reminderName, ActorType: actorType, ActorID: o.actorID})
		if gerr == nil && rem != nil {
			log.Debugf("Workflow actor '%s': reminder '%s' is registered, detached arm not needed", o.actorID, reminderName)
			return
		}

		err = o.reminders.Create(ctx, req)
		if err == nil {
			log.Infof("Workflow actor '%s': reminder '%s' created by the detached retry", o.actorID, reminderName)
			if armedStatus != "" {
				diag.DefaultWorkflowMonitoring.WorkflowLocalWake(context.Background(), armedStatus)
			}
			return
		}
		if ctx.Err() != nil {
			log.Warnf("Workflow actor '%s': detached create of reminder '%s' stopped by shutdown; a status read re-drives the instance on the next owner", o.actorID, reminderName)
			diag.DefaultWorkflowMonitoring.WorkflowLocalWake(context.Background(), diag.StatusArmDetachedSkipped)
			return
		}
		if common.IsPermanentCreateError(err) && status.Code(err) != codes.ResourceExhausted && !errors.Is(err, reminders.ErrReminderOpActorNotHosted) {
			log.Errorf("Workflow actor '%s': detached create of reminder '%s' gave up, the instance stays committed without a driver until a status read re-drives it: %v", o.actorID, reminderName, err)
			diag.DefaultWorkflowMonitoring.WorkflowLocalWake(context.Background(), diag.StatusArmDetachedFailed)
			return
		}

		select {
		case <-ctx.Done():
		case <-time.After(bo.NextBackOff()):
		}
	}
}

// redriveOverduePendingStart re-asserts the start reminder of a
// saved-but-never-run instance whose start is overdue, at most once per grace
// per residency. Status reads call it because they are the only thing that
// touches a stranded instance when the client does not re-issue the create.
func (o *orchestrator) redriveOverduePendingStart(state *wfenginestate.State) {
	now := time.Now()
	pending := pendingstart.Overdue(state, now)
	if pending == nil {
		return
	}

	last := o.lastStartRedrive.Load()
	if last != 0 && now.Sub(time.Unix(0, last)) < pendingstart.RedriveGrace() {
		return
	}
	if !o.lastStartRedrive.CompareAndSwap(last, now.UnixNano()) {
		return
	}

	due := pendingstart.DueTime(pending)
	name := events.EventReminderName(reminderPrefixStart, pending)
	log.Debugf("Workflow actor '%s': pending start due at %s has not run after %s, checking its start reminder", o.actorID, due.UTC().Format(time.RFC3339Nano), now.Sub(due).Round(time.Millisecond))
	o.armReminderDetached(name, due, pending.GetExecutionStarted().GetName(), startStillPending(name), diag.StatusPendingStartRedriven)
}

// redriveWhenOverdue re-checks a parked status wait once the pending start it
// observed becomes overdue, since the wait is otherwise woken only by a commit
// or a deactivation. The returned stop is called when the wait ends.
func (o *orchestrator) redriveWhenOverdue(pending *backend.HistoryEvent, sf *streamFn) func() {
	delay := time.Until(pendingstart.DueTime(pending).Add(pendingstart.RedriveGrace()))
	timer := time.AfterFunc(max(delay, 0), func() {
		if sf.done.Load() {
			return
		}
		o.detached.Go(func(ctx context.Context) {
			state, err := wfenginestate.LoadWorkflowState(ctx, o.actorState, o.actorID, o.stateOptions())
			if err != nil || state == nil {
				return
			}
			o.redriveOverduePendingStart(state)
		})
	})
	return func() { timer.Stop() }
}
