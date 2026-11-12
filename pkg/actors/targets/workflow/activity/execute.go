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

package activity

import (
	"context"
	"errors"
	"fmt"
	"time"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/activity/claim"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/activity/inflight"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api/protos"
)

// errStaleClaimEvicted settles an evicted stale inflight call. Recoverable:
// waiters parked on the evicted call surface it into their retry chains,
// which re-arrive and follow the fresh execution.
var errStaleClaimEvicted = wferrors.NewRecoverable(errors.New(
	"in-flight activity claim evicted as stale (its work item is no longer held by the engine); re-executing"))

// executeActivity runs one activity delivery. A scheduler-fired reminder is a
// recovery arrival that can land on a fresh placement owner mid-handoff, so
// under the fast path a fresh owner consults the durable execution-claim
// record first (see gate). A local drive sets SkipRetries and stays ungated:
// handleInvoke gates its janitor re-dispatch before arming it.
func (a *activity) executeActivity(ctx context.Context, reminder *actorapi.Reminder, invocation *protos.ActivityInvocation) error {
	taskEvent := invocation.GetHistoryEvent()
	ts := taskEvent.GetTaskScheduled()
	if ts == nil {
		return fmt.Errorf("invalid activity task event: '%s'", taskEvent.String())
	}
	activityName := ts.GetName()

	workflowID, err := a.workflowID()
	if err != nil {
		return err
	}

	// Activities are stateless workers with no ext-sigcert table to absorb
	// certs into, so propagated history is verify-or-reject. The helper covers
	// the disabled-signer and nil-payload cases.
	if err := a.signing.VerifyPropagatedHistoryStateless(invocation.GetPropagatedHistory()); err != nil {
		return fmt.Errorf("activity '%s::%d' rejecting invocation: propagated history verification failed: %w", activityName, taskEvent.GetEventId(), err)
	}

	key := inflight.Key(a.actorID, taskEvent)
	taskID := taskEvent.GetEventId()
	gated := a.fastPath && !reminder.SkipRetries

	// Under the fast path a local drive that runs out of retries creates
	// the run-activity reminder, unordered with a purge of its workflow.
	// Ack such a reminder rather than run the activity of a purged workflow.
	if gated {
		purged, perr := a.parentPurged(ctx, workflowID, invocation)
		if perr != nil {
			return wferrors.NewRecoverable(fmt.Errorf("failed to read the workflow metadata row: %w", perr))
		}
		if purged {
			log.Infof("Activity actor '%s': workflow '%s' was purged; dropping its run-activity reminder", a.actorID, workflowID)
			diag.DefaultWorkflowMonitoring.WorkflowLocalActivity(context.Background(), diag.StatusPurgedDropped)
			return nil
		}
	}
	for {
		call, owner, err := a.claim(ctx, key, workflowID, taskID, reminder.SkipLock)
		if err != nil {
			return err
		}
		if owner {
			if gated {
				if proceed, gerr := a.gate(ctx, key, call); !proceed {
					return gerr
				}
			}
			return a.runOwned(ctx, key, call, activityName, workflowID, taskEvent, invocation)
		}

		// Another arrival owns this scheduling (in flight, or its outcome is
		// still cached): follow it and surface the same outcome, so the
		// scheduler's retry acks without dispatching the activity again.
		log.Debugf("Activity actor '%s': following in-flight execution of '%s'", a.actorID, reminder.Name)
		if stale, ferr := a.follow(ctx, call, workflowID, taskID); !stale {
			if errors.Is(ferr, errResultUndelivered) {
				return nil
			}
			return ferr
		}
		// claim() evicts the stale entry (unblocking parked followers into
		// their retry chains) and re-contends for ownership.
	}
}

// follow parks on an in-flight call owned by another arrival. stale means the
// claim died while we waited and the caller must re-contend through claim(),
// which is what evicts it. The staleness predicate is re-sampled here because
// a claim can turn stale AFTER its followers arrive (the owner's work item
// lost mid-wait), and outside the fast path no janitor re-dispatch would ever
// re-evaluate it.
func (a *activity) follow(ctx context.Context, call *inflight.Call, workflowID string, taskID int32) (stale bool, err error) {
	ticker := time.NewTicker(a.staleClaimAfter)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-call.Done():
			return false, call.Err()
		case <-ticker.C:
			if a.staleClaim(call, workflowID, taskID) {
				return true, nil
			}
		}
	}
}

// claim acquires the inflight entry for key, taking the actor lock for the
// claim only unless the caller asked to skip it. The lock MUST NOT extend past
// the claim: what follows is the app roundtrip (arbitrary length) and the
// result delivery into the parent workflow (contends on the parent's turn
// lock), and holding the per-actor lock across either parks Execute
// dispatches mesh-wide behind slow parent turns. Neither needs the actor's
// serialization: the inflight entry dedups duplicate arrivals, and a crash
// mid-execution is recovered by the parent janitor re-dispatching the
// unresolved TaskScheduled event.
func (a *activity) claim(ctx context.Context, key, workflowID string, taskID int32, skipLock bool) (*inflight.Call, bool, error) {
	if !skipLock {
		unlock, err := a.lock.ContextLock(ctx)
		if err != nil {
			return nil, false, err
		}
		defer unlock()
	}

	for {
		call, owner := a.inflight.Acquire(key)
		if owner || !a.staleClaim(call, workflowID, taskID) {
			return call, owner, nil
		}

		// The claim belongs to a dead execution: its work item left the
		// engine without resolving, so nothing will ever settle it. Following
		// it strands the activity while the janitor re-dispatches every period
		// to no effect (the janitor-livelock class). Evict it so this arrival
		// re-executes as a fresh owner; a late completion of the evicted
		// execution is dropped by the orchestrator's duplicate dedup.
		if !call.TryEvict() {
			// The owner entered resolve between the staleness read and here:
			// the execution is publishing its result and must not be evicted.
			return call, owner, nil
		}
		a.settle(key, call, errStaleClaimEvicted)
		log.Warnf("Activity actor '%s': evicted a stale in-flight claim (no engine-held work item after %s); re-executing", a.actorID, call.Age())
		diag.DefaultWorkflowMonitoring.WorkflowLocalActivity(context.Background(), diag.StatusClaimEvicted)
	}
}

// staleClaim reports whether an inflight claim is provably dead: unsettled,
// not resolving, older than the stale grace, and with no engine-held work
// item for it. The resolving phase covers the gap between the engine
// releasing its held registration and the result publish settling the call
// (the publish contends on the parent's turn lock and must never read as
// stale). A live execution of any length keeps its registration, so it is
// never stale regardless of age. The grace is two janitor periods: it must
// exceed the registration latency of a freshly-claimed dispatch, including
// one parked on the engine handoff under load, before the first janitor
// re-dispatch can observe it; an eviction under more extreme delay degrades
// to a duplicate execution absorbed by the orchestrator's dedup.
func (a *activity) staleClaim(call *inflight.Call, workflowID string, taskID int32) bool {
	if call.Settled() || call.Resolving() || call.Age() < a.staleClaimAfter {
		return false
	}
	return a.executionHeld != nil && !a.executionHeld(workflowID, taskID)
}

// gate classifies the durable execution-claim record (see the claim
// subpackage) for a recovery arrival about to execute key. proceed=false means
// do not execute here, and err is what the arrival surfaces: nil acks (the
// guarded execution already completed and published), recoverable defers
// (live elsewhere, or unreadable record). owned is the inflight entry the
// arrival already holds, if any; it is settled with the same outcome so the
// followers parked on it read it too.
func (a *activity) gate(ctx context.Context, key string, owned *inflight.Call) (proceed bool, err error) {
	outcome, cerr := a.claims.Check(ctx, a.actorID, key)
	switch {
	case cerr != nil:
		err = wferrors.NewRecoverable(fmt.Errorf("failed to read the execution-claim record: %w", cerr))
	case outcome == claim.Defer:
		log.Infof("Activity actor '%s': the execution claim is live on another host; deferring re-execution", a.actorID)
		err = claim.ErrHeldElsewhere
	case outcome == claim.Completed:
		log.Infof("Activity actor '%s': the execution completed on its previous host; acking without re-executing", a.actorID)
	default:
		return true, nil
	}
	if owned != nil {
		a.settle(key, owned, err)
	}
	return false, err
}

// purgeConfirmWindow is how long after a task was scheduled a missing
// workflow metadata row is not yet taken as a purge. The row is written when
// the workflow is created, before any task is scheduled, so a store whose
// reads lag its writes can still be missing it for a young task. Dropping the
// reminder of a live workflow can strand it: its janitor is only asserted by
// a fast path orchestrator.
const purgeConfirmWindow = 5 * time.Second

// parentPurged reports whether the activity's workflow has no state left. An
// empty read is confirmed once, as a store may not yet serve its own write
// (see the orchestrator's saveInternalState), and is an error, for the
// reminder to retry, while the task is younger than purgeConfirmWindow. False
// for a workflow of another app, whose state this daprd cannot read.
func (a *activity) parentPurged(ctx context.Context, workflowID string, invocation *protos.ActivityInvocation) (bool, error) {
	if src := invocation.GetHistoryEvent().GetRouter().GetSourceAppID(); src != "" && src != a.appID {
		return false, nil
	}
	req := &actorapi.GetStateRequest{
		ActorType: a.workflowActorType,
		ActorID:   workflowID,
		Key:       wfenginestate.MetadataKey,
	}
	for range 2 {
		res, err := a.state.Get(ctx, req, false)
		if err != nil {
			return false, err
		}
		if res == nil || len(res.Data) > 0 {
			return false, nil
		}
	}
	if age := time.Since(invocation.GetHistoryEvent().GetTimestamp().AsTime()); age < purgeConfirmWindow {
		return false, fmt.Errorf("workflow '%s' has no metadata row %s after its task was scheduled; not yet taken as purged", workflowID, age.Truncate(time.Millisecond))
	}
	return true, nil
}
