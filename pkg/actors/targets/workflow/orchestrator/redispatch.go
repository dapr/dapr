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

	diag "github.com/dapr/dapr/pkg/diagnostics"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/backend"
)

// redispatchCallTimeout bounds one janitor re-dispatch Execute call. A call
// that finds the activity actor lock held by a still-running execution parks
// until the lock frees; the timeout converts that into a benign "busy"
// outcome (the activity is in flight, no recovery needed, the next janitor
// period re-checks). It MUST stay below the activity inflight cache TTL
// (60s): a parked call that proceeds just after the execution completes
// must land inside the cached-outcome window and become a follower, never a
// fresh execution.
const redispatchCallTimeout = 30 * time.Second

// redispatchSuppressed defers the re-dispatch pass only while a drive is
// mid-flight on THIS instance: its commit may resolve tasks momentarily, and
// the deferral is bounded (the drive ends and the next fire re-checks).
// Instance-wide progress is deliberately NOT a suppression signal: sibling
// activities or external events can keep committing forever while one
// activity host is dead, and per-task liveness is what the re-dispatch
// itself probes (a live execution on the target host absorbs it as a
// follower; only a lost one re-executes).
func (o *orchestrator) redispatchSuppressed() bool {
	return o.driveRunning.Load()
}

// redispatchActivities re-dispatches TaskScheduled events whose resolution
// has not been observed. It is the durable re-driver for in-flight
// activities: under WorkflowsFastPath an activity host crash
// leaves nothing durable on the activity side, and the committed
// TaskScheduled event plus this re-dispatch restore exactly the coverage the
// elided run-activity reminder provided, within one janitor period. It also
// recovers pre-existing exposures of the reminder path (scheduler job loss,
// a completion publish terminally lost after the reminder was acked), so it
// runs on every janitor fire regardless of the activity gate.
//
// Re-dispatch is at-least-once safe: the same persisted event is sent, so
// the activity actor ID and inflight key line up (a concurrent execution on
// the target host absorbs the duplicate as a follower) and a duplicate
// completion is dropped by the orchestrator's dedup.
//
// The Execute calls MUST NOT run inline: this is called from a janitor fire
// that holds the orchestrator lock, an Execute call can park on the activity
// actor lock behind a running execution, and that execution's completion
// publish needs the orchestrator lock, a cross-actor cycle that would only
// break on timeout. Everything touching orchestrator state is captured
// synchronously; the sends run detached under the factory wake context,
// drained by HaltAll.
func (o *orchestrator) redispatchActivities(ctx context.Context, state *wfenginestate.State, unresolved []*backend.HistoryEvent) {
	// unresolved events come from state.History, so History is non-empty;
	// mirror callActivities' dueTime derivation.
	dueTime := state.History[0].GetTimestamp().AsTime()
	wfName := o.getExecutionStartedEvent(state).GetName()

	// Rebuild the propagated history chunks the original dispatch carried:
	// the propagation scope is persisted on the TaskScheduled event, so the
	// re-dispatch preserves it (same mechanism as rerun).
	phs, err := buildRerunOutgoingHistory(unresolved, state, o.actorID, o.appID, taskScheduledScope)
	if err != nil {
		log.Warnf("Workflow actor '%s': failed to assemble propagated history for activity re-dispatch; the next janitor period retries: %v", o.actorID, err)
		diag.DefaultWorkflowMonitoring.WorkflowLocalActivity(context.Background(), diag.StatusJanitorRedispatchFailed)
		return
	}
	for _, ph := range phs {
		if err = o.signing.SignOutgoingPropagatedHistory(ph, o.appID); err != nil {
			log.Warnf("Workflow actor '%s': failed to sign propagated history for activity re-dispatch; the next janitor period retries: %v", o.actorID, err)
			diag.DefaultWorkflowMonitoring.WorkflowLocalActivity(context.Background(), diag.StatusJanitorRedispatchFailed)
			return
		}
	}

	// The janitor that led here just fired, so its backstop is demonstrably
	// armed: the elision certification only depends on the gate.
	elide := o.fastPath

	o.wakeLock.Lock()
	wakeCtx := o.wakeCtx
	if wakeCtx.Err() != nil {
		o.wakeLock.Unlock()
		// Placement churn or shutdown: the next owner's janitor re-checks.
		return
	}
	o.wakeWG.Add(1)
	o.wakeLock.Unlock()

	// An elided re-dispatch is fire-and-forget by construction: a nil Execute
	// error certifies only that the target host accepted the call and armed a
	// detached local drive (activity/invoke.go). Everything after that
	// instant is invisible to this host, and a loss (the arm dying with its
	// pod at a placement handoff before claiming a work item) leaves nothing
	// durable, because the accepted call was also the elision certification;
	// repeating the same unverified dispatch every period repeats the loss.
	// A task still
	// unresolved at the fire AFTER a re-dispatch was attempted has outlived
	// the only proof acceptance carries, so it escalates: dispatched WITHOUT
	// the local-drive certification, making the target host create the
	// durable run-activity reminder the fast path elided. The reminder
	// survives placement churn by construction (host-agnostic, retry-forever)
	// and its fire verifies against the engine's held-execution oracle (claim
	// in activity/execute.go): a live execution absorbs it as a follower and
	// runs once, a lost one re-executes fresh. Escalation repeats each period
	// while the task stays unresolved; the create is idempotent
	// (overwrite-by-name) and stops with resolution. This part runs
	// synchronously: janitor fires hold the turn lock, which guards the map.
	durable := make(map[int32]bool, len(unresolved))
	if elide {
		// Task IDs restart from zero each ContinueAsNew generation: a map
		// built against an older generation would treat a new task's first
		// re-dispatch as already-attempted and escalate it straight to the
		// durable reminder.
		if o.janitorRedispatchedGen != state.Generation {
			o.janitorRedispatched = nil
			o.janitorEscalated = nil
			o.janitorRedispatchedGen = state.Generation
		}
		if o.janitorRedispatched == nil {
			o.janitorRedispatched = make(map[int32]struct{}, len(unresolved))
		}
		live := make(map[int32]struct{}, len(unresolved))
		for _, e := range unresolved {
			id := e.GetEventId()
			live[id] = struct{}{}
			if _, ok := o.janitorRedispatched[id]; ok {
				durable[id] = true
				if o.janitorEscalated == nil {
					o.janitorEscalated = make(map[int32]*backend.HistoryEvent)
				}
				o.janitorEscalated[id] = e
			} else {
				o.janitorRedispatched[id] = struct{}{}
			}
		}
		// Prune resolved tasks so later tasks of a long-lived instance get
		// their own first-attempt local re-dispatch, not a stale escalation.
		for id := range o.janitorRedispatched {
			if _, ok := live[id]; !ok {
				delete(o.janitorRedispatched, id)
			}
		}
	}

	go func() {
		defer o.wakeWG.Done()
		for _, e := range unresolved {
			cctx, cancel := context.WithTimeout(wakeCtx, redispatchCallTimeout)
			cerr := o.callActivity(cctx, e, dueTime, phs[e.GetEventId()], wfName, elide && !durable[e.GetEventId()], true)
			cancel()
			switch {
			case cerr == nil && durable[e.GetEventId()]:
				log.Infof("Workflow actor '%s': janitor escalated unresolved activity '%s::%d' to its durable run-activity reminder (a prior re-dispatch did not resolve it)", o.actorID, e.GetTaskScheduled().GetName(), e.GetEventId())
				diag.DefaultWorkflowMonitoring.WorkflowLocalActivity(context.Background(), diag.StatusJanitorRedispatchEscalated)
				o.reapResolvedEscalation(wakeCtx, e)
			case cerr == nil:
				log.Infof("Workflow actor '%s': janitor re-dispatched unresolved activity '%s::%d'", o.actorID, e.GetTaskScheduled().GetName(), e.GetEventId())
				diag.DefaultWorkflowMonitoring.WorkflowLocalActivity(context.Background(), diag.StatusJanitorRedispatched)
			case errors.Is(cerr, context.DeadlineExceeded):
				// The activity actor is busy executing: exactly the healthy
				// in-flight case, nothing to recover.
				diag.DefaultWorkflowMonitoring.WorkflowLocalActivity(context.Background(), diag.StatusJanitorRedispatchBusy)
			default:
				log.Warnf("Workflow actor '%s': janitor re-dispatch of activity '%s::%d' failed; the next janitor period retries: %v", o.actorID, e.GetTaskScheduled().GetName(), e.GetEventId(), cerr)
				diag.DefaultWorkflowMonitoring.WorkflowLocalActivity(context.Background(), diag.StatusJanitorRedispatchFailed)
			}
		}
	}()
}
