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

package chaos

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/durabletask-go/task"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/fault"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(orphancompletion))
}

// orphancompletion exercises the failure mode in which the workflow's own
// state save fails on the run that schedules an activity. Under the legacy
// dispatch-before-save order, the activity actor has already been triggered
// (and its "run-activity" reminder is durable in the scheduler) by the time
// the rollback happens. The activity runs to completion, posts TaskCompleted
// back to the workflow via the activity-result reminder which inserts it
// into the workflow's inbox via addWorkflowEvent (a separate state-store
// transaction that does not hit the injected fault, so it commits).
//
// On retry the workflow reloads: history has no TaskScheduled (rolled back)
// but inbox carries an orphan TaskCompleted. The workflow code's
// deterministic replay re-emits ScheduleTask with the same EventId, and the
// dispatcher's IsTaskAlreadyResolved check silently skips the new dispatch
// because the resolution is already present in NewEvents. The unconsumed
// TaskCompleted is appended to history by ApplyRuntimeStateChanges, leaving
// an out-of-order completion that the workflow can never match against an
// await. The workflow stalls forever.
//
// With save-before-dispatch, the activity is not dispatched until the save
// commits. A save failure cancels the run before any side effect, so no
// orphan TaskCompleted can ever be produced. The workflow's reminder retry
// re-runs the execution cleanly against the rolled-back state.
//
// The redispatch helper covers the complementary edge case where the save
// succeeds but the dispatch call (router.Call) fails afterwards: the next
// runWorkflow scans state.History for TaskScheduled without matching
// TaskCompleted / TaskFailed and re-invokes the activity actor. Dispatch is
// idempotent (fixed run-activity reminder name with upsert semantics +
// inflight.Acquire coalescing).
type orphancompletion struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
}

func (o *orphancompletion) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	o.store = fault.New(t)

	sock := socket.New(t)
	o.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(o.store),
	)

	o.workflow = workflow.New(t,
		workflow.WithNoDB(),
		workflow.WithDaprdOptions(0,
			daprd.WithSocket(t, sock),
			daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.%s
  version: v1
  metadata:
  - name: actorStateStore
    value: "true"
`, o.ss.SocketName())),
		),
	)

	return []framework.Option{
		framework.WithProcesses(o.ss, o.workflow),
	}
}

func (o *orphancompletion) Run(t *testing.T, ctx context.Context) {
	o.workflow.WaitUntilRunning(t, ctx)

	const wfID = "orphan-wf"

	var activityRuns atomic.Int32
	releaseActivity := make(chan struct{})

	r := o.workflow.Registry()
	require.NoError(t, r.AddActivityN("act", func(actx task.ActivityContext) (any, error) {
		activityRuns.Add(1)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-releaseActivity:
			return activityResultDone, nil
		}
	}))

	require.NoError(t, r.AddWorkflowN("wf", func(octx *task.WorkflowContext) (any, error) {
		var out string
		if err := octx.CallActivity("act").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))

	o.workflow.BackendClient(t, ctx)
	gclient := o.workflow.GRPCClient(t, ctx)

	// Arm a single failure on the workflow's history- save. This is the
	// save that captures the just-emitted TaskScheduled action. With
	// save-before-dispatch, the activity is dispatched AFTER this save
	// commits; the injected failure on the first attempt therefore prevents
	// the dispatch entirely, and the retry runs cleanly against the
	// rolled-back state.
	failedCh := make(chan struct{})
	o.store.ArmFailures(wfID+"||history-", 1, failedCh)

	_, err := gclient.StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "wf",
		InstanceId:        wfID,
	})
	require.NoError(t, err)

	// Wait until the injected save failure has fired at least once, then
	// release the activity so the retry can complete.
	select {
	case <-failedCh:
	case <-time.After(15 * time.Second):
		require.Fail(t, "injected history-save failure never fired")
	}
	close(releaseActivity)

	// Workflow must complete.  Pre-fix, the orphan TaskCompleted would
	// strand the workflow in RUNNING forever and this assertion would time
	// out.
	assert.EventuallyWithT(t, func(co *assert.CollectT) {
		resp, gerr := gclient.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{
			InstanceId:        wfID,
			WorkflowComponent: "dapr",
		})
		if assert.NoError(co, gerr) {
			assert.Equal(co, "COMPLETED", resp.GetRuntimeStatus())
		}
	}, 45*time.Second, 100*time.Millisecond)

	// The activity must have run AT LEAST once (it may run more if the
	// retry path triggers re-dispatch). What matters is that exactly one
	// TaskCompleted ends up matched against exactly one TaskScheduled in
	// history; the workflow completes only if that pairing happens.
	assert.GreaterOrEqual(t, activityRuns.Load(), int32(1),
		"activity must have executed for the workflow to complete")

	assert.GreaterOrEqual(t, o.store.FailedCount(), 1,
		"expected the injected history-save failure to have fired")
}
