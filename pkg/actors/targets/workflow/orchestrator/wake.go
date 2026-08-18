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
	"time"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	diag "github.com/dapr/dapr/pkg/diagnostics"
)

// localWakeTimeout bounds a single detached local wake attempt, including the
// time it spends queued on the actor lock behind the arming invocation.
const localWakeTimeout = time.Minute

// escalateTimeout bounds the durable-reminder create performed when a local
// drive fails. It is deliberately generous: the create is idempotent
// (overwrite-by-name) and host-agnostic, and the janitor remains the net if
// it also fails.
const escalateTimeout = 30 * time.Second

// driveInfo carries the identity of the latest wake so a drive (or its
// escalation) can name the reminder it stands in for. Any new-event-prefixed
// name drives a full inbox drain, so latest-wins is sufficient.
type driveInfo struct {
	reminderName string
	dueTime      time.Time
	wfName       string
}

// localDrive eagerly drives a wake-up on this host instead of creating a
// per-event scheduler reminder, cutting both the scheduler trigger-delivery
// leg AND the reminder's job upsert/delete commit pair out of the workflow
// hot path.
//
// MUST be called only after BOTH the state save AND a durable re-driver are
// in place: either the per-instance janitor reminder (ensureJanitor) or a
// durable one-shot reminder created by the caller (assertStartReminder keeps
// its scheduler entry for delayed starts and pending-start recovery).
//
// Wakes are delivered through a per-instance DRIVE LOOP: localDrive posts a
// notification (buffered-1 channel: pending notifications coalesce, mirroring
// the scheduler's overwrite-by-name semantics this path replaces) and spawns
// the loop if none is running. The loop runs one turn per pending
// notification and exits when idle, with a reclaim handshake that makes
// notification loss impossible: a notify posted concurrently with loop exit
// is either consumed by the exiting loop's recheck or observed by its poster,
// who spawns a fresh loop. A drive drains the WHOLE durable inbox, so any
// event saved before its turn starts is covered by that turn.
//
// The loop is detached (the arming invocation holds the actor lock the turn
// needs) and scoped to the factory's wake context, drained in HaltAll. On a
// drive error it ESCALATES by creating today's durable per-event reminder
// (deterministic name, idempotent) on a context bounded by the factory root
// context, NOT wakeCtx: migration is exactly the case where wakeCtx is
// cancelled, and the reminder create is host-agnostic (the scheduler routes
// the fire to the current owner). If the escalation also fails, the janitor
// drives recovery within one period.
//
// No-op when the WorkflowsFastPath preview feature is off or the
// wake is scheduled in the future (delayed starts must keep their scheduler
// due time); callers fall back to the durable per-event reminder path.
func (o *orchestrator) localDrive(reminderName string, dueTime time.Time, wfName string) {
	if !o.fastPath || dueTime.After(time.Now()) {
		return
	}

	o.driveInfo.Store(&driveInfo{reminderName: reminderName, dueTime: dueTime, wfName: wfName})

	// Post the wake. A full buffer means a notification is already pending
	// and this wake coalesces into it: the pending drive's turn runs after
	// this event's (already committed) inbox save.
	select {
	case o.driveNotify <- struct{}{}:
	default:
	}

	if !o.driveRunning.CompareAndSwap(false, true) {
		// A loop is running; the posted notification is consumed either by
		// it or by the reclaim handshake in driveLoop.
		return
	}

	// Serialize the spawn against HaltAll's cancel/recreate cycle: either
	// the Add happens before the cancel (and HaltAll waits for this loop),
	// or the context is already cancelled and the spawn is skipped; the
	// janitor (or the caller's durable reminder) drives the turn on the new
	// owner.
	o.wakeLock.Lock()
	wakeCtx := o.wakeCtx
	if wakeCtx.Err() != nil {
		o.wakeLock.Unlock()
		o.driveRunning.Store(false)
		return
	}
	o.wakeWG.Add(1)
	o.wakeLock.Unlock()

	go o.driveLoop(wakeCtx)
}

// driveLoop consumes drive notifications for this instance, running one turn
// per notification. It never blocks on the notification channel (HaltAll
// safety: cancellation surfaces through the turn call), exits when idle, and
// on a failed turn hands over to the escalation path and exits: the durable
// reminder (or janitor) owns recovery from there.
func (o *orchestrator) driveLoop(wakeCtx context.Context) {
	defer o.wakeWG.Done()

	actorType := o.actorTypeBuilder.Workflow(o.appID)
	actorID := o.actorID

	for {
		select {
		case <-o.driveNotify:
		default:
			// No pending work: release the running slot, then re-check for
			// a notification that raced the release. If one is found, try
			// to reclaim the slot; losing the reclaim means another loop
			// has started, so re-post the notification for it.
			o.driveRunning.Store(false)
			select {
			case <-o.driveNotify:
				if !o.driveRunning.CompareAndSwap(false, true) {
					select {
					case o.driveNotify <- struct{}{}:
					default:
					}
					return
				}
				// Slot reclaimed; run this notification.
			default:
				return
			}
		}

		info := o.driveInfo.Load()

		ctx, cancel := context.WithTimeout(wakeCtx, localWakeTimeout)
		start := time.Now()
		// Data is nil: wake-up reminders carry no payload; the turn reloads
		// the durable inbox. The router resolves placement, so if the actor
		// migrated between arming and wake the turn is delivered to the new
		// owner host. SkipRetries: this loop owns its recovery (a failed
		// drive escalates to a durable reminder within ~1s), so the router's
		// blind 1s-backoff retries would only add tail latency before the
		// same outcome.
		err := o.router.CallReminder(ctx, &actorapi.Reminder{
			Name:        info.reminderName,
			ActorType:   actorType,
			ActorID:     actorID,
			SkipRetries: true,
		})
		elapsed := float64(time.Since(start)) / float64(time.Millisecond)
		cancel()

		if err != nil {
			log.Debugf("Workflow actor '%s': local wake '%s' failed; escalating to a durable reminder: %v", actorID, info.reminderName, err)
			diag.DefaultWorkflowMonitoring.WorkflowLocalWake(context.Background(), diag.StatusFailed)
			diag.DefaultWorkflowMonitoring.WorkflowLocalWakeDrive(context.Background(), diag.StatusFailed, elapsed)
			o.driveRunning.Store(false)
			o.escalate(info)
			return
		}

		diag.DefaultWorkflowMonitoring.WorkflowLocalWake(context.Background(), diag.StatusSuccess)
		diag.DefaultWorkflowMonitoring.WorkflowLocalWakeDrive(context.Background(), diag.StatusSuccess, elapsed)
	}
}

// escalate creates the durable per-event wake-up reminder after a failed
// local drive, restoring exactly today's non-fast-path recovery chain. It is
// detached from wakeCtx (see localDrive) and tracked by escWG, which is
// waited only at factory close so placement-churn HaltAll latency is
// unaffected.
func (o *orchestrator) escalate(info *driveInfo) {
	o.escLock.Lock()
	rootCtx := o.rootCtx
	if rootCtx.Err() != nil {
		o.escLock.Unlock()
		// Process shutdown: nothing to escalate from; the janitor (which
		// survives in the scheduler) drives recovery on the next owner.
		diag.DefaultWorkflowMonitoring.WorkflowLocalWake(context.Background(), diag.StatusEscalateSkipped)
		return
	}
	o.escWG.Add(1)
	o.escLock.Unlock()

	go func() {
		defer o.escWG.Done()

		ctx, cancel := context.WithTimeout(rootCtx, escalateTimeout)
		defer cancel()

		if err := o.createWorkflowReminderForever(ctx, info.reminderName, nil, info.dueTime, o.appID, &info.wfName); err != nil {
			// The janitor remains the durable net: recovery within one
			// janitor period instead of ~1s.
			log.Warnf("Workflow actor '%s': failed to escalate wake '%s' to a durable reminder; the janitor will drive it: %v", o.actorID, info.reminderName, err)
			diag.DefaultWorkflowMonitoring.WorkflowLocalWake(context.Background(), diag.StatusEscalateFailed)
			return
		}
		diag.DefaultWorkflowMonitoring.WorkflowLocalWake(context.Background(), diag.StatusEscalated)
	}()
}
