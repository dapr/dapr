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
	"strings"
	"time"

	actorsapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/activity/inflight"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/messages"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	"github.com/dapr/durabletask-go/api/protos"
)

// errStaleClaimEvicted settles an evicted stale inflight call. Recoverable:
// waiters parked on the evicted call surface it into their retry chains,
// which re-arrive and follow the fresh execution.
var errStaleClaimEvicted = wferrors.NewRecoverable(errors.New(
	"in-flight activity claim evicted as stale (its work item is no longer held by the engine); re-executing"))

func (a *activity) executeActivity(ctx context.Context, name string, invocation *protos.ActivityInvocation, skipLock bool) error {
	taskEvent := invocation.GetHistoryEvent()
	activityName := ""
	if ts := taskEvent.GetTaskScheduled(); ts != nil {
		activityName = ts.GetName()
	} else {
		return fmt.Errorf("invalid activity task event: '%s'", taskEvent.String())
	}

	endIndex := strings.Index(a.actorID, "::")
	if endIndex < 0 {
		return fmt.Errorf("invalid activity actor ID: '%s'", a.actorID)
	}
	workflowID := a.actorID[0:endIndex]

	// Cryptographically verify any propagated history before letting the
	// activity see it. Activities are stateless workers with no
	// ext-sigcert table to absorb certs into, so this is a verify-or-
	// reject gate. The helper handles the disabled-signer case (logs a
	// warning if a signed payload arrives) and the nil-payload case
	// internally. On failure, abort activity execution: the caller (parent
	// workflow) gets a recoverable error and the activity never runs.
	if err := a.signing.VerifyPropagatedHistoryStateless(invocation.GetPropagatedHistory()); err != nil {
		return fmt.Errorf("activity '%s::%d' rejecting invocation: propagated history verification failed: %w", activityName, taskEvent.GetEventId(), err)
	}

	key := inflight.Key(a.actorID, taskEvent)
	for {
		call, owner, err := a.claim(ctx, key, workflowID, taskEvent.GetEventId(), skipLock)
		if err != nil {
			return err
		}
		if owner {
			return a.runOwned(ctx, key, call, name, activityName, workflowID, taskEvent, invocation)
		}

		// A previous reminder for this activity scheduling is already in
		// flight (or just finished and its outcome is still cached). Wait
		// for its result and surface the same outcome so the scheduler's
		// retry can be acked without dispatching the activity to the SDK
		// again. The owner is responsible for posting the result to the
		// workflow actor. Staleness is re-checked while parked: the claim
		// can turn stale AFTER followers arrive (the owner's work item lost
		// mid-wait), and claim() only evicts at claim time.
		log.Debugf("Activity actor '%s': following in-flight execution of '%s'", a.actorID, name)
		if a.staleClaimAfter <= 0 {
			// No eviction grace configured (test harnesses): park without
			// the staleness recheck.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-call.Done():
				return call.Err()
			}
		}
		stale := false
		ticker := time.NewTicker(a.staleClaimAfter)
		for !stale {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return ctx.Err()
			case <-call.Done():
				ticker.Stop()
				return call.Err()
			case <-ticker.C:
				stale = a.staleClaim(call, workflowID, taskEvent.GetEventId())
			}
		}
		ticker.Stop()
		// Loop back into claim(): it evicts the stale entry (unblocking
		// every parked follower into their retry chains) and re-contends
		// for ownership, so this arrival can re-execute as a fresh owner.
	}
}

// claim acquires the inflight entry for key, taking the actor lock for the
// claim only unless the caller asked to skip it.
// The lock MUST NOT extend past the claim: the segments that follow are the
// app roundtrip (arbitrary length) and the result delivery into the parent
// workflow (contends on the parent's turn lock), and holding the per-actor
// lock across either parks Execute dispatches mesh-wide behind slow parent
// turns. Neither segment needs the actor's serialization: the inflight entry
// dedups duplicate arrivals (they join as followers, locked or not), and a
// crash mid-execution is recovered by the parent janitor re-dispatching the
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
		// engine without resolving (completion or cancellation delivery lost
		// to a stream break), so nothing will ever settle it. Following it
		// strands the activity forever while the janitor re-dispatches every
		// period to no effect (the janitor-livelock class). Evict it so this
		// arrival re-executes as a fresh owner: the eviction error unblocks
		// parked followers into their retry chains, and a completion of the
		// evicted execution arriving late is dropped by the orchestrator's
		// duplicate-completion dedup.
		call.Finish(errStaleClaimEvicted)
		a.inflight.Release(key, call)
		log.Warnf("Activity actor '%s': evicted a stale in-flight claim (no engine-held work item after %s); re-executing", a.actorID, call.Age())
		diag.DefaultWorkflowMonitoring.WorkflowLocalActivity(context.Background(), diag.StatusClaimEvicted)
	}
}

// staleClaim reports whether an inflight claim is provably dead: unsettled,
// not resolving, older than the stale grace, and with no engine-held work
// item for it. The resolving phase covers the gap between the engine
// releasing its held registration (work item completed) and the result
// publish settling the call: the publish contends on the parent workflow's
// turn lock and must never read as stale. A
// live execution of any length keeps its completion registration in the
// engine, so it is never stale regardless of age (long-running activities
// are not re-executed). The grace is two janitor periods: it must exceed the
// registration latency of a freshly-claimed dispatch, including one parked on
// the engine handoff under load, before the first janitor re-dispatch can
// observe it. Eviction of a claim whose work item is still queued pre-
// registration is therefore possible only under extreme handoff delay, and
// degrades to a duplicate execution absorbed by the orchestrator's dedup
// (at-least-once, the same guarantee the durable reminder path provides).
func (a *activity) staleClaim(call *inflight.Call, workflowID string, taskID int32) bool {
	if call.Settled() || call.Resolving() || call.Age() < a.staleClaimAfter {
		return false
	}
	return a.executionHeld != nil && !a.executionHeld(workflowID, taskID)
}

func (f *factory) actorNotReachable(ctx context.Context, wfActorType, workflowID string) bool {
	_, _, cancel, err := f.placement.LookupActor(ctx, &actorsapi.LookupActorRequest{
		ActorType: wfActorType,
		ActorID:   workflowID,
	})
	if cancel != nil {
		cancel(nil)
	}
	return errors.Is(err, messages.ErrActorNoAddress)
}
