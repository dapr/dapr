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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/proxy"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

// retentionJobName is the scheduler job the orchestrator creates to retire a
// terminal instance; failing it is what keeps the actor resident.
const retentionJobName = "retention"

func init() {
	suite.Register(new(awaitduplicate))
}

// awaitduplicate pins the ID-reuse guard against a duplicate completion. A
// terminal instance refuses reuse of its ID while an activity result is still
// owed, and only the result that settles the await clears the guard. A
// duplicate of an already-resolved task id settles nothing: the result the
// guard stands for belongs to another task and is still in flight, so a
// duplicate that cleared the guard would hand the ID to a fresh generation
// with the previous one's activity still running.
//
// The guard lives on the orchestrator object rather than in the store, so it
// is observable only while the terminal actor is still the object that armed
// it, and a terminal turn ends by deactivating the actor. The retention
// reminder is therefore made to fail: settleTerminal never succeeds, the
// terminal turn returns its error instead of RunCompletedTrue, and the
// resident actor keeps retrying with the instance already durably complete.
// The terminal status is read from the log rather than from the runtime
// status API, which deactivates the actor the moment it reads terminal
// metadata.
type awaitduplicate struct {
	workflow *workflow.Workflow
	proxy    *proxy.Proxy
	logline  *logline.LogLine
}

func (a *awaitduplicate) Setup(t *testing.T) []framework.Option {
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

func (a *awaitduplicate) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	const id = "awaitduplicate"

	releaseTwo := make(chan struct{})
	releaseTwoOnce := sync.OnceFunc(func() { close(releaseTwo) })
	t.Cleanup(releaseTwoOnce)
	twoStarted := make(chan struct{})
	markTwoStarted := sync.OnceFunc(func() { close(twoStarted) })

	// Sequential on purpose: one's completion clears the guard, and two's
	// dispatch on the terminal turn re-arms it, so the guard is genuinely set
	// with a result genuinely owed when the duplicate of one's completion
	// lands. two is scheduled and never awaited, so the workflow completes
	// while it runs.
	reg := a.workflow.Registry()
	require.NoError(t, reg.AddActivityN("one", func(task.ActivityContext) (any, error) {
		return "one", nil
	}))
	require.NoError(t, reg.AddActivityN("two", func(task.ActivityContext) (any, error) {
		markTwoStarted()
		<-releaseTwo
		return "two", nil
	}))
	require.NoError(t, reg.AddWorkflowN("awaitduplicate", func(wctx *task.WorkflowContext) (any, error) {
		if err := wctx.CallActivity("one").Await(nil); err != nil {
			return nil, err
		}
		wctx.CallActivity("two")
		return "done", nil
	}))

	// Every attempt to (re)create the retention reminder fails, so the
	// terminal turn never settles and the actor stays resident. The code must
	// be one the orchestrator's create helper treats as permanent, or the
	// helper spends a minute of backoff inside the turn holding the lock.
	a.proxy.ArmNamedFailures(proxy.MethodScheduleJob, retentionJobName, 10000, codes.InvalidArgument, nil)

	client := a.workflow.BackendClient(t, ctx)
	_, err := client.ScheduleNewWorkflow(ctx, "awaitduplicate", api.WithInstanceID(id))
	require.NoError(t, err)
	select {
	case <-twoStarted:
	case <-time.After(time.Second * 20):
		require.Fail(t, "timed out waiting for the second activity to start")
	}
	require.Eventually(t, func() bool {
		return a.logline.Contains("Workflow Actor '" + id + "': workflow completed with status 'ORCHESTRATION_STATUS_COMPLETED'")
	}, time.Second*20, time.Millisecond*10, "the workflow must complete with two still running")
	require.Eventually(t, func() bool { return a.proxy.FailedCount() > 0 }, time.Second*20, time.Millisecond*10,
		"the retention reminder must fail so the terminal actor is not deactivated")

	hist, err := client.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	var execOne string
	for _, ev := range hist.GetEvents() {
		if ts := ev.GetTaskScheduled(); ts != nil && ev.GetEventId() == 0 {
			execOne = ts.GetTaskExecutionId()
		}
	}
	require.NotEmpty(t, execOne, "task 0 must be recorded with an execution id")

	// The activity actor's durable retry, replayed: task 0's own result,
	// already in history. Dedup keys on kind and id, so this classifies as a
	// duplicate however faithfully it names the execution.

	const dupReminder = common.ReminderPrefixActivityResult + "dup"
	appID := a.workflow.Dapr().AppID()
	fworkflow.PlantReminder(t, ctx, a.workflow.Scheduler().Client(t, ctx), appID, id, dupReminder, &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{
				TaskScheduledId: 0,
				TaskExecutionId: execOne,
				Result:          wrapperspb.String(`"one"`),
			},
		},
	})

	require.Eventually(t, func() bool {
		return a.logline.Contains("Workflow actor '" + id + "': dropping duplicate completion already in history/inbox")
	}, time.Second*20, time.Millisecond*10, "the duplicate must reach the admission path")
	a.workflow.Scheduler().WaitJobKeyCount(t, ctx, dupReminder, func(n int) bool { return n == 0 })

	_, err = client.ScheduleNewWorkflow(ctx, "awaitduplicate", api.WithInstanceID(id))
	require.ErrorContains(t, err, "is already awaiting an activity result",
		"the ID must stay blocked while the second activity's result is owed")

	meta, err := client.FetchWorkflowMetadata(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus(),
		"the refused create must not have started a new generation")
	assert.JSONEq(t, `"done"`, meta.GetOutput().GetValue())
}
