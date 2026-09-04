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

	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common/pendingstart"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/events"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
)

// armDetachedOnCreateError hands a wake-up reminder create that failed after
// its inbox row was committed to a detached retry, unless the error is a
// permanent request error. The invocation context is the placement claim
// context, which a dissemination round cancels regardless of the Scheduler's
// health, so a context error must not abandon the row's only driver.
func (o *orchestrator) armDetachedOnCreateError(reminderName string, start time.Time, wfName string, err error) {
	ctxErr := errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
	if !ctxErr && common.IsPermanentCreateError(err) {
		return
	}
	log.Warnf("Workflow actor '%s': failed to create reminder '%s' after its inbox row was committed, retrying detached from the invocation: %v", o.actorID, reminderName, err)
	o.armReminderDetached(reminderName, start, wfName)
}

// armReminderDetached creates the deterministic wake-up reminder on the root
// context, retrying until it succeeds, a permanent error surfaces, or the
// runtime shuts down. The create is host-agnostic and an overwrite-by-name, so
// it is safe after the actor has been deactivated or moved. Hand-offs for a
// name already in flight collapse onto the running goroutine.
func (o *orchestrator) armReminderDetached(reminderName string, start time.Time, wfName string) {
	if _, inflight := o.armPending.LoadOrStore(reminderName, struct{}{}); inflight {
		return
	}

	started := o.detached.Go(func(rootCtx context.Context) {
		defer o.armPending.Delete(reminderName)

		if err := o.createWorkflowReminderForever(rootCtx, reminderName, nil, start, o.appID, &wfName); err != nil {
			log.Errorf("Workflow actor '%s': detached create of reminder '%s' gave up, the instance stays committed without a driver until a status read re-drives it: %v", o.actorID, reminderName, err)
			diag.DefaultWorkflowMonitoring.WorkflowLocalWake(context.Background(), diag.StatusArmDetachedFailed)
			return
		}
		log.Infof("Workflow actor '%s': reminder '%s' created by the detached retry", o.actorID, reminderName)
	})
	if !started {
		o.armPending.Delete(reminderName)
		return
	}
	diag.DefaultWorkflowMonitoring.WorkflowLocalWake(context.Background(), diag.StatusArmDetached)
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
	log.Warnf("Workflow actor '%s': pending start due at %s has not run after %s, re-asserting its start reminder", o.actorID, due.UTC().Format(time.RFC3339Nano), now.Sub(due).Round(time.Millisecond))
	diag.DefaultWorkflowMonitoring.WorkflowLocalWake(context.Background(), diag.StatusPendingStartRedriven)
	o.armReminderDetached(events.EventReminderName(reminderPrefixStart, pending), due, pending.GetExecutionStarted().GetName())
}
