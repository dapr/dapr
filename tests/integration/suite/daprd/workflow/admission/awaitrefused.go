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

package admission

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/proxy"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(awaitrefused))
}

// awaitrefused is awaitstranded's other half. There the orphan reaches a
// COMPLETED instance and is acknowledged and dropped; here the successor
// generation is still RUNNING when the orphan reports, so the same verdict
// is returned as a recoverable refusal instead and the drop happens on the
// sender, after its retry window, with nothing left to re-deliver the result.
//
// The guard against reusing the ID cannot be released on that verdict: a
// refused result is routinely admitted by a later retry once a lagging read
// catches up, which admission/staleverdict pins. It is released instead once
// the window the sender spends has passed, because only then can no
// redelivery exist. Held as a verdict-only guard, the entry armed for a task
// id the successor passed without scheduling was never released at all and
// every create for the ID failed.
//
// The publish window is shortened so the release is observable inside the
// case budget, the retention reminder is failed so the terminal actor stays
// resident with its guard, and the fast path is pinned off because its
// janitor re-dispatches unresolved schedulings.
type awaitrefused struct {
	workflow *workflow.Workflow
	logline  *logline.LogLine
	proxy    *proxy.Proxy
}

func (a *awaitrefused) Setup(t *testing.T) []framework.Option {
	sched := scheduler.New(t, scheduler.WithID("dapr-scheduler-server-0"))
	a.proxy = proxy.New(t, sched)
	a.logline = logline.New(t, logline.WithCaptureAll())
	a.workflow = workflow.New(t,
		workflow.WithFastPath(false),
		workflow.WithSigning(false),
		workflow.WithSchedulerInstance(sched),
		workflow.WithSchedulerAddress(a.proxy.Address()),
		workflow.WithDaprdOptions(0,
			daprd.WithExecOptions(
				exec.WithStdout(a.logline.Stdout()), exec.WithStderr(a.logline.Stderr()),
				exec.WithEnvVars(t, "DAPR_WORKFLOW_TEST_ACTIVITY_PUBLISH_RETRY_WINDOW", "2s"),
			),
			daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: wfretention
spec:
  workflow:
    stateRetentionPolicy:
      anyTerminal: "1h"
`),
		),
	)

	return []framework.Option{
		framework.WithProcesses(a.logline, sched, a.proxy, a.workflow),
	}
}

func (a *awaitrefused) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	const id = "awaitrefused"
	orphanStarted := make(chan struct{})
	releaseOrphan := make(chan struct{})
	markStarted := sync.OnceFunc(func() { close(orphanStarted) })
	releaseOnce := sync.OnceFunc(func() { close(releaseOrphan) })
	t.Cleanup(releaseOnce)

	reg := a.workflow.Registry()
	require.NoError(t, reg.AddActivityN("orphan", func(task.ActivityContext) (any, error) {
		markStarted()
		<-releaseOrphan
		return "orphan-done", nil
	}))
	require.NoError(t, reg.AddWorkflowN("awaitrefused", func(wctx *task.WorkflowContext) (any, error) {
		var input string
		if err := wctx.GetInput(&input); err != nil {
			return nil, err
		}
		switch input {
		case "first":
			// Scheduled at task id 0 and never awaited: orphaned below.
			wctx.CallActivity("orphan")
			if err := wctx.WaitForSingleEvent("proceed", time.Minute).Await(nil); err != nil {
				return nil, err
			}
			wctx.ContinueAsNew("second")
			return nil, nil
		case "second":
			// A timer takes task id 0 in this generation, so the orphan's
			// result finds the id passed with no activity scheduled there.
			if err := wctx.CreateTimer(time.Millisecond).Await(nil); err != nil {
				return nil, err
			}
			// Still RUNNING when the orphan reports, so its result is
			// refused rather than acknowledged.
			if err := wctx.WaitForSingleEvent("finish", time.Minute).Await(nil); err != nil {
				return nil, err
			}
			return "second-done", nil
		}
		return "fresh", nil
	}))

	// Every attempt to (re)create the retention reminder fails, so the
	// terminal turn never settles and the actor stays resident with its
	// guard. InvalidArgument so the create helper treats it as permanent.
	a.proxy.ArmNamedFailures(proxy.MethodScheduleJob, retentionJobName, 10000, codes.InvalidArgument, nil)

	client := a.workflow.BackendClient(t, ctx)
	_, err := client.ScheduleNewWorkflow(ctx, "awaitrefused", api.WithInstanceID(id), api.WithInput("first"))
	require.NoError(t, err)

	select {
	case <-orphanStarted:
	case <-time.After(time.Second * 20):
		require.Fail(t, "timed out waiting for the orphaned activity to start")
	}
	require.NoError(t, client.RaiseEvent(ctx, id, "proceed"))

	// The orphan reports to a generation that is still running, so it is
	// refused and the sender retries it in hand.
	releaseOnce()
	retried := fmt.Sprintf("result publish for workflow '%s' refused, retrying with the result in hand", id)
	require.Eventually(t, func() bool { return a.logline.Contains(retried) }, time.Second*20, time.Millisecond*10,
		"the orphan's result must be refused as superseded and retried in hand")

	// The sender must exhaust its window and drop the result BEFORE the
	// generation completes. Otherwise a later retry reaches a terminal
	// instance, takes the acknowledged-drop path and settles the entry
	// there, which is awaitstranded's case and not this one.
	dropped := fmt.Sprintf("dropping the result for workflow '%s', still superseded after the retry window", id)
	require.Eventually(t, func() bool { return a.logline.Contains(dropped) }, time.Second*20, time.Millisecond*10,
		"the sender must give up on the refused result while the generation is still running")

	require.NoError(t, client.RaiseEvent(ctx, id, "finish"))
	require.Eventually(t, func() bool {
		return a.logline.Contains("Workflow Actor '" + id + "': workflow completed with status 'ORCHESTRATION_STATUS_COMPLETED'")
	}, time.Second*20, time.Millisecond*10, "the second generation must complete")
	require.Eventually(t, func() bool { return a.proxy.FailedCount() > 0 }, time.Second*20, time.Millisecond*10,
		"the retention reminder must fail so the terminal actor is not deactivated")

	// Once the sender's window has passed nothing can re-deliver the refused
	// result, so the ID stops being held.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, cerr := client.ScheduleNewWorkflow(ctx, "awaitrefused", api.WithInstanceID(id), api.WithInput("third"))
		assert.NoError(c, cerr)
	}, time.Second*20, time.Millisecond*100,
		"the ID must be reusable once no redelivery of the refused result can exist")

	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus(), "%v", meta.GetFailureDetails())
	assert.Equal(t, `"fresh"`, meta.GetOutput().GetValue())
}
