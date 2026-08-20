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
	targeterrors "github.com/dapr/dapr/pkg/actors/targets/errors"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
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

// Escalation hysteresis defaults. A failed drive against an instance that
// shows recent life is retried in place, and if still failing is handed to the
// janitor rather than escalated.
const (
	driveRetryBudget        = 6 * time.Second
	defaultDriveAliveWindow = 3 * time.Second
)

func (o *orchestrator) aliveWithin(window time.Duration) bool {
	cutoff := time.Now().Add(-window).UnixNano()
	return o.lastProgress.Load() >= cutoff || o.lastActive.Load() >= cutoff
}

func (o *orchestrator) progressWithin(window time.Duration) bool {
	return o.lastProgress.Load() >= time.Now().Add(-window).UnixNano()
}

func (o *orchestrator) driveLost(wakeCtx context.Context, err error) bool {
	return wakeCtx.Err() != nil || o.closed.Load() || targeterrors.IsClosed(err)
}

// driveRetrySchedule yields the waits before each in-place retry of a failed
// drive: the factory-injected fixed schedule when set (tests), otherwise
// decorrelated exponential jitter bounded by driveRetryBudget of total sleep.
type driveRetrySchedule struct {
	fixed  []time.Duration
	idx    int
	jitter *common.JitterBackoff
	slept  time.Duration
}

func (o *orchestrator) newDriveRetrySchedule() *driveRetrySchedule {
	if o.driveRetryBackoffs != nil {
		return &driveRetrySchedule{fixed: o.driveRetryBackoffs}
	}
	return &driveRetrySchedule{
		jitter: common.NewJitterBackoff(common.RetryBackoffBase, common.RetryBackoffCap),
	}
}

func (s *driveRetrySchedule) next() (time.Duration, bool) {
	if s.fixed != nil {
		if s.idx >= len(s.fixed) {
			return 0, false
		}
		d := s.fixed[s.idx]
		s.idx++
		return d, true
	}

	d := s.jitter.NextBackOff()
	if s.slept+d > driveRetryBudget {
		return 0, false
	}
	s.slept += d
	return d, true
}

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
// drive error it first retries in place (bounded; see driveRetrySchedule),
// then either ESCALATES by creating today's durable per-event reminder
// (deterministic name, idempotent) on a context bounded by the factory root
// context, NOT wakeCtx: migration is exactly the case where wakeCtx is
// cancelled, and the reminder create is host-agnostic (the scheduler routes
// the fire to the current owner); or, when the instance still shows recent
// life, SUPPRESSES the escalation and leaves recovery to the janitor (see
// driveLoop). Hard errors (cancelled wakeCtx, closed actor) always escalate.
// If the escalation also fails, the janitor drives recovery within one
// period.
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
// on a failed turn (after the bounded in-place retries) hands over to the
// escalation decision and exits: the durable reminder (or janitor) owns
// recovery from there.
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

		err := o.driveOnce(wakeCtx, actorType, actorID, info)

		// Bounded in-place retries: a live instance's failed drive is
		// re-attempted here (same coverage, no scheduler involvement)
		// instead of escalating on first failure. Retries stop early when
		// the drive is lost outright or the instance stops showing life.
		retries := o.newDriveRetrySchedule()
		for err != nil {
			if o.driveLost(wakeCtx, err) || !o.aliveWithin(o.driveAliveWindow) {
				break
			}
			d, ok := retries.next()
			if !ok {
				break
			}
			if !sleepWake(wakeCtx, d) {
				break
			}
			err = o.driveOnce(wakeCtx, actorType, actorID, info)
		}

		if err != nil {
			o.driveRunning.Store(false)
			if o.driveLost(wakeCtx, err) || !o.aliveWithin(o.driveAliveWindow) {
				log.Debugf("Workflow actor '%s': local wake '%s' failed; escalating to a durable reminder: %v", actorID, info.reminderName, err)
				o.escalate(info)
			} else {
				// Alive and slow: a durable reminder here would only add
				// scheduler re-drive load against an actor that is already
				// working. The janitor drives any stranded inbox row within
				// one period; that is the recovery contract this suppression
				// leans on.
				log.Debugf("Workflow actor '%s': local wake '%s' failed but the instance shows recent progress; suppressing escalation, the janitor covers: %v", actorID, info.reminderName, err)
				diag.DefaultWorkflowMonitoring.WorkflowLocalWake(context.Background(), diag.StatusEscalateSuppressed)
			}
			return
		}
	}
}

func (o *orchestrator) driveOnce(wakeCtx context.Context, actorType, actorID string, info *driveInfo) error {
	ctx, cancel := context.WithTimeout(wakeCtx, localWakeTimeout)
	start := time.Now()
	err := o.router.CallReminder(ctx, &actorapi.Reminder{
		Name:        info.reminderName,
		ActorType:   actorType,
		ActorID:     actorID,
		SkipRetries: true,
	})
	elapsed := float64(time.Since(start)) / float64(time.Millisecond)
	cancel()

	status := diag.StatusSuccess
	if err != nil {
		status = diag.StatusFailed
	}
	diag.DefaultWorkflowMonitoring.WorkflowLocalWake(context.Background(), status)
	diag.DefaultWorkflowMonitoring.WorkflowLocalWakeDrive(context.Background(), status, elapsed)
	return err
}

// escalate creates the durable per-event wake-up reminder after a failed
// local drive, restoring exactly today's non-fast-path recovery chain. It is
// detached from wakeCtx (see localDrive) and tracked by escWG for tests;
// production waits it nowhere (the goroutines are rootCtx+timeout bounded),
// so placement-churn HaltAll latency is unaffected.
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

func sleepWake(wakeCtx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-wakeCtx.Done():
		return false
	case <-t.C:
		return true
	}
}
