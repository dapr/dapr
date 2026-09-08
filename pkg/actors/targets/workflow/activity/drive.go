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
// actor lock the execution's claim needs, and the execution can run for an
// arbitrary length of time while the orchestrator's dispatch must unblock
// immediately.
// It is scoped to the factory drive context, drained in HaltAll. Delivering
// the execution through router.CallReminder re-enters the normal
// InvokeReminder path, so locking, inflight dedup, error classification and
// deactivation are identical to a reminder fire.
func (a *activity) localDrive(invocation *protos.ActivityInvocation, dueTime time.Time, activityName *string) bool {
	f := a.factory

	if dropActivityDriveForTest() {
		log.Warnf("TEST INJECTION: dropping the local drive arm for activity actor '%s'", a.actorID)
		return true
	}

	f.driveLock.Lock()
	driveCtx := f.driveCtx
	if driveCtx.Err() != nil {
		f.driveLock.Unlock()
		return false
	}
	f.driveWG.Add(1)
	f.driveLock.Unlock()

	go func() {
		defer f.driveWG.Done()
		f.driveActivity(driveCtx, a.actorID, invocation, dueTime, activityName)
	}()

	return true
}

// testDropActivityDrives is a test-only fault injection: the first N activity
// local drive arms report success without spawning their drive goroutine,
// reproducing a work item lost between the arm and the execution claim (the
// arming host dying at a placement handoff: no completion, no claim, no fold
// entry, nothing durable, because the accepted Execute call was also the
// elision certification). Both the initial dispatch and the janitor's elided
// re-dispatch arm through here, so a budget covering the test window models
// a re-dispatch lost the same way as the dispatch. Not a supported
// production knob.
var testDropActivityDrives = sync.OnceValue(func() int64 {
	v := os.Getenv("DAPR_WORKFLOW_TEST_DROP_ACTIVITY_DRIVES")
	if v == "" {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		log.Warnf("Ignoring invalid DAPR_WORKFLOW_TEST_DROP_ACTIVITY_DRIVES %q", v)
		return 0
	}
	return n
})

// droppedActivityDrives counts the local drive arms dropped by the test-only
// DAPR_WORKFLOW_TEST_DROP_ACTIVITY_DRIVES injection.
var droppedActivityDrives atomic.Int64

func dropActivityDriveForTest() bool {
	budget := testDropActivityDrives()
	if budget == 0 {
		return false
	}
	return droppedActivityDrives.Add(1) <= budget
}

// driveActivity runs one activity execution locally, retrying transient
// failures at the same cadence as the elided reminder's failure policy, and
// escalates to the durable run-activity reminder when the drive cannot
// complete here (repeated failure, or driveCtx cancellation on placement
// churn or shutdown, where a host-agnostic reminder create is exactly what
// is wanted). If the escalation also fails, the workflow janitor
// re-dispatches the unresolved task within one period.
func (f *factory) driveActivity(driveCtx context.Context, actorID string, invocation *protos.ActivityInvocation, dueTime time.Time, activityName *string) {
	anydata, err := anypb.New(invocation)
	if err != nil {
		// Unreachable for a just-decoded invocation; keep durability anyway.
		log.Errorf("Activity actor '%s': failed to marshal invocation for local drive: %v", actorID, err)
		f.escalateActivity(actorID, invocation, dueTime, activityName)
		return
	}

	// SkipRetries: this drive owns its recovery (bounded local retries, then
	// escalation to the durable reminder), so the router's blind 1s-backoff
	// retries would only delay it. SkipLock stays false: the execution claim
	// takes the activity actor lock like any locked reminder fire, and the
	// lock is released before the app roundtrip (see claim in execute.go).
	reminder := &actorapi.Reminder{
		Name:        activityReminderName,
		ActorType:   f.actorType,
		ActorID:     actorID,
		Data:        anydata,
		SkipRetries: true,
	}

	// Local retries back off with the shared decorrelated jitter bounded by
	// [common.RetryBackoffBase, common.RetryBackoffCap): the run-activity
	// reminder policy this drive replaces is itself a per-job random draw
	// from that range (RetryForeverPolicy), so a fixed interval here would
	// be MORE synchronized than the path it elides, re-colliding concurrent
	// failed drives against a struggling app.
	bo := common.NewJitterBackoff(common.RetryBackoffBase, common.RetryBackoffCap)

	// No per-attempt deadline: activities run for arbitrary lengths and a
	// reminder-fired execution is equally unbounded. The bound is driveCtx.
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

		if driveCtx.Err() != nil || attempt >= localDriveMaxAttempts {
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
	if call, ok := f.inflight.Peek(key); ok && !call.Settled() {
		log.Infof("Activity actor '%s': local drive aborted but its execution claim is live; skipping the durable-reminder escalation", actorID)
		diag.DefaultWorkflowMonitoring.WorkflowLocalActivity(context.Background(), diag.StatusEscalateSkipped)
		return
	}

	log.Warnf("Activity actor '%s': local drive failed; escalating to a durable run-activity reminder: %v", actorID, err)
	diag.DefaultWorkflowMonitoring.WorkflowLocalActivity(context.Background(), diag.StatusFailed)
	f.escalateActivity(actorID, invocation, dueTime, activityName)
}

// escalateActivity creates the durable run-activity reminder after a failed
// local drive, restoring the non-fast-path recovery chain. It is detached
// from driveCtx (see driveActivity) on the factory's detached runner and
// bounded by the root context plus escalateTimeout; the runner is not waited
// on the placement-churn path so HaltAll latency is unaffected.
func (f *factory) escalateActivity(actorID string, invocation *protos.ActivityInvocation, dueTime time.Time, activityName *string) {
	started := f.detached.Go(func(rootCtx context.Context) {
		ctx, cancel := context.WithTimeout(rootCtx, escalateTimeout)
		defer cancel()

		if err := f.createActivityReminder(ctx, actorID, invocation, dueTime, activityName); err != nil {
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
