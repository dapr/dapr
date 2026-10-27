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

package activity

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/types/known/anypb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/activity/inflight"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/durabletask-go/api/protos"
)

const (
	// localDriveMaxAttempts bounds local retries before escalating to the
	// durable run-activity reminder, which restores exactly the
	// retry-forever chain of the non-fast-path.
	localDriveMaxAttempts = 3

	// escalateTimeout bounds the durable-reminder create performed when a
	// local drive fails. The create is idempotent (overwrite-by-name) and
	// host-agnostic, and the workflow janitor remains the net if it also
	// fails.
	escalateTimeout = 30 * time.Second
)

// localDrive begins executing a certified activity invocation on this host in
// place of the elided run-activity reminder fire. It returns false when the
// drive cannot be armed (the factory is halting), in which case the caller
// MUST fall back to creating the durable reminder.
//
// The drive is detached: the arming Execute invocation holds the activity
// actor lock the execution's claim needs, and the orchestrator's dispatch
// must unblock immediately. It is scoped to the churn-scoped drive runner,
// drained in HaltAll, which is also what refuses the arm once that scope is
// cancelled. Delivering through router.CallReminder re-enters the normal
// InvokeReminder path, so locking, inflight dedup, error classification and
// deactivation are identical to a reminder fire.
func (a *activity) localDrive(invocation *protos.ActivityInvocation, activityName *string) bool {
	if dropActivityDriveForTest() {
		log.Warnf("TEST INJECTION: dropping the local drive arm for activity actor '%s'", a.actorID)
		return true
	}

	f, actorID := a.factory, a.actorID
	return f.driveScope().Go(func(driveCtx context.Context) {
		f.driveActivity(driveCtx, actorID, invocation, activityName)
	})
}

// testBudget returns a predicate that reports true for the first n calls,
// with n read once from the named test-only fault-injection variable (unset
// or 0 disables it). Not a supported production knob.
func testBudget(env string) func() bool {
	var used atomic.Int64
	budget := sync.OnceValue(func() int64 {
		v := os.Getenv(env)
		if v == "" {
			return 0
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			log.Warnf("Ignoring invalid %s %q", env, v)
			return 0
		}
		return n
	})
	return func() bool {
		n := budget()
		return n != 0 && used.Add(1) <= n
	}
}

// dropActivityDriveForTest: the first N local drive arms report success
// without spawning their drive goroutine, reproducing a work item lost between
// the arm and the execution claim (the arming host dying at a placement
// handoff: no completion, no claim, nothing durable, because the accepted
// Execute call was also the elision certification). Both the initial dispatch
// and the janitor's elided re-dispatch arm through here.
var dropActivityDriveForTest = testBudget("DAPR_WORKFLOW_TEST_DROP_ACTIVITY_DRIVES")

// driveActivity runs one activity execution locally, retrying transient
// failures at the same cadence as the elided reminder's failure policy, and
// escalates to the durable run-activity reminder when the drive cannot
// complete here (repeated failure, or driveCtx cancellation on placement
// churn or shutdown, where a host-agnostic reminder create is exactly what
// is wanted). If the escalation also fails, the workflow janitor
// re-dispatches the unresolved task within one period.
func (f *factory) driveActivity(driveCtx context.Context, actorID string, invocation *protos.ActivityInvocation, activityName *string) {
	anydata, err := anypb.New(invocation)
	if err != nil {
		// Unreachable for a just-decoded invocation; keep durability anyway.
		log.Errorf("Activity actor '%s': failed to marshal invocation for local drive: %v", actorID, err)
		f.escalateActivity(actorID, invocation, activityName)
		return
	}

	// SkipRetries: this drive owns its recovery (bounded local retries, then
	// escalation to the durable reminder), so the router's blind 1s-backoff
	// retries would only delay it. SkipLock stays false: the execution claim
	// takes the actor lock and releases it before the app roundtrip.
	reminder := &actorapi.Reminder{
		Name:        activityReminderName,
		ActorType:   f.actorType,
		ActorID:     actorID,
		Data:        anydata,
		SkipRetries: true,
	}

	// Jittered like the run-activity reminder policy this drive replaces
	// (RetryForeverPolicy draws from the same range); a fixed interval would
	// re-collide concurrent failed drives against a struggling app. No
	// per-attempt deadline: activities run for arbitrary lengths, the bound
	// is driveCtx.
	bo := common.NewJitterBackoff(common.RetryBackoffBase, common.RetryBackoffCap)
	for attempt := 1; ; attempt++ {
		start := time.Now()
		err = f.router.CallReminder(driveCtx, reminder)
		elapsed := float64(time.Since(start)) / float64(time.Millisecond)

		if err == nil {
			diag.DefaultWorkflowMonitoring.WorkflowLocalActivity(context.Background(), diag.StatusSuccess)
			diag.DefaultWorkflowMonitoring.WorkflowLocalActivityDrive(context.Background(), diag.StatusSuccess, elapsed)
			return
		}
		diag.DefaultWorkflowMonitoring.WorkflowLocalActivityDrive(context.Background(), diag.StatusFailed, elapsed)

		// A cancelled invocation was cut by the actor lifecycle (a placement
		// drain force-cancel), not by the app: the execution and its publish
		// watcher stay live here. A retry would route to the new placement
		// owner, which runs the body again once the claim record's retention
		// lapses.
		if driveCtx.Err() != nil || attempt >= localDriveMaxAttempts || errors.Is(err, context.Canceled) {
			break
		}

		select {
		case <-driveCtx.Done():
		case <-time.After(bo.NextBackOff()):
			continue
		}
		break
	}

	// A churn-aborted drive with a live claim has not lost the work: the
	// detached publish watcher delivers it. Escalating would plant a
	// reminder that can duplicate the body on a new placement owner; skip,
	// the janitor re-dispatch covers a lost delivery.
	key := inflight.Key(actorID, invocation.GetHistoryEvent())
	if call, ok := f.inflight.Peek(key); ok {
		if !call.Settled() {
			log.Infof("Activity actor '%s': local drive aborted but its execution claim is live; skipping the durable-reminder escalation", actorID)
			diag.DefaultWorkflowMonitoring.WorkflowLocalActivity(context.Background(), diag.StatusEscalateSkipped)
			return
		}
		if call.Err() == nil {
			// The cancelled invocation's watcher already published.
			return
		}
	}

	log.Warnf("Activity actor '%s': local drive failed; escalating to a durable run-activity reminder: %v", actorID, err)
	diag.DefaultWorkflowMonitoring.WorkflowLocalActivity(context.Background(), diag.StatusFailed)
	f.escalateActivity(actorID, invocation, activityName)
}

// escalateActivity creates the durable run-activity reminder after a failed
// local drive, restoring the non-fast-path recovery chain. It runs on the
// runtime-scoped runner rather than the drive scope, bounded by
// escalateTimeout, so HaltAll latency is unaffected. The reminder is due now:
// a drive is only ever armed for an invocation whose dueTime has passed.
func (f *factory) escalateActivity(actorID string, invocation *protos.ActivityInvocation, activityName *string) {
	started := f.detached.Go(func(rootCtx context.Context) {
		ctx, cancel := context.WithTimeout(rootCtx, escalateTimeout)
		defer cancel()

		if err := f.createActivityReminder(ctx, actorID, invocation, time.Now(), activityName); err != nil {
			log.Warnf("Activity actor '%s': failed to escalate to a durable run-activity reminder; the workflow janitor re-dispatches within one period: %v", actorID, err)
			diag.DefaultWorkflowMonitoring.WorkflowLocalActivity(context.Background(), diag.StatusEscalateFailed)
			return
		}
		diag.DefaultWorkflowMonitoring.WorkflowLocalActivity(context.Background(), diag.StatusEscalated)
	})
	if !started {
		// Process shutdown: the workflow janitor (which survives in the
		// scheduler) re-dispatches on the next owner.
		diag.DefaultWorkflowMonitoring.WorkflowLocalActivity(context.Background(), diag.StatusEscalateSkipped)
	}
}
