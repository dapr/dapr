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
	suite.Register(new(awaitstranded))
}

// awaitstranded is awaitduplicate's opposite: the guard against reusing a
// completed instance's ID must be RELEASED by the arrival of the result it
// stands for, whatever the instance then does with that result.
//
// The generation that dispatched the activity is replaced by one that
// schedules a timer at the same task id and calls no activity of its own, so
// nothing re-arms the guard and nothing admits a result. The orphan's result
// is then acknowledged and dropped as "passed id 0 without scheduling a
// task", which is not this generation's own result. Held as one flag cleared
// only by the current scheduling's own result, the guard was never released
// and every create for the ID failed.
//
// The guard lives on the orchestrator object, so it is observable only while
// the terminal actor is still resident: the retention reminder is made to
// fail so the terminal turn never settles, exactly as awaitduplicate does.
// The fast path is pinned off because its janitor re-dispatches unresolved
// schedulings, which would arm the guard again.
type awaitstranded struct {
	workflow *workflow.Workflow
	logline  *logline.LogLine
	proxy    *proxy.Proxy
}

func (a *awaitstranded) Setup(t *testing.T) []framework.Option {
	sched := scheduler.New(t, scheduler.WithID("dapr-scheduler-server-0"))
	a.proxy = proxy.New(t, sched)
	a.logline = logline.New(t, logline.WithCaptureAll())
	a.workflow = workflow.New(t,
		workflow.WithFastPath(false),
		workflow.WithSigning(false),
		workflow.WithSchedulerInstance(sched),
		workflow.WithSchedulerAddress(a.proxy.Address()),
		workflow.WithDaprdOptions(0,
			daprd.WithExecOptions(exec.WithStdout(a.logline.Stdout()), exec.WithStderr(a.logline.Stderr())),
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

func (a *awaitstranded) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	const id = "awaitstranded"
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
	require.NoError(t, reg.AddWorkflowN("awaitstranded", func(wctx *task.WorkflowContext) (any, error) {
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
			return "second-done", nil
		}
		return "fresh", nil
	}))

	// Every attempt to (re)create the retention reminder fails, so the
	// terminal turn never settles and the actor stays resident with its
	// guard. InvalidArgument so the create helper treats it as permanent.
	a.proxy.ArmNamedFailures(proxy.MethodScheduleJob, retentionJobName, 10000, codes.InvalidArgument, nil)

	client := a.workflow.BackendClient(t, ctx)
	_, err := client.ScheduleNewWorkflow(ctx, "awaitstranded", api.WithInstanceID(id), api.WithInput("first"))
	require.NoError(t, err)

	select {
	case <-orphanStarted:
	case <-time.After(time.Second * 20):
		require.Fail(t, "timed out waiting for the orphaned activity to start")
	}
	require.NoError(t, client.RaiseEvent(ctx, id, "proceed"))

	require.Eventually(t, func() bool {
		return a.logline.Contains("Workflow Actor '" + id + "': workflow completed with status 'ORCHESTRATION_STATUS_COMPLETED'")
	}, time.Second*20, time.Millisecond*10, "the second generation must complete with the orphan still running")
	require.Eventually(t, func() bool { return a.proxy.FailedCount() > 0 }, time.Second*20, time.Millisecond*10,
		"the retention reminder must fail so the terminal actor is not deactivated")

	// Only now does the orphan report, to a completed instance whose history
	// passed its task id without scheduling an activity there.
	releaseOnce()
	dropped := fmt.Sprintf("Workflow actor '%s': dropping completion (sender ''): this generation passed id 0 without scheduling a task", id)
	require.Eventually(t, func() bool { return a.logline.Contains(dropped) }, time.Second*20, time.Millisecond*10,
		"the orphan's result must be acknowledged and dropped")

	// The result the guard stood for has been judged, so nothing is owed.
	_, err = client.ScheduleNewWorkflow(ctx, "awaitstranded", api.WithInstanceID(id), api.WithInput("third"))
	require.NoError(t, err, "reusing the ID once the orphan's result has been judged must succeed")
	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus(), "%v", meta.GetFailureDetails())
	assert.Equal(t, `"fresh"`, meta.GetOutput().GetValue())
}
