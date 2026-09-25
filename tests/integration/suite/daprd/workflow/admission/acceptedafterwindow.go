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

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/fault"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(acceptedafterwindow))
}

// acceptedafterwindow pins what the in-hand retry loop returns once its
// window has expired. The loop remembers the last refusal so a call cut by
// the window reports the verdict rather than the expiry, and it used to
// substitute that remembered refusal for ANY return once the window was
// gone, an accepted delivery included. A delivery the orchestrator accepted
// was then reported to the activity actor as the stale refusal and dropped,
// losing a result whose inbox row had already committed. The loop must
// return the call's own error unchanged.
//
// The nil-return half of that is a nanosecond race (the window expiring
// between the call returning nil and the check) and is pinned by a unit
// test. This covers its sibling, which is constructible: a call whose inbox
// row commits while the window expires underneath it. The commit is parked
// inside the store, holding the actor lock, until the window is certainly
// gone.
//
// The delivery has to be one the orchestrator would otherwise refuse, so
// that the loop has a refusal to remember: the recorded scheduling of task 0
// is rewritten underneath the actor's warm cache, exactly as in staleverdict,
// and put back before the admission. This test therefore also depends on the
// refusal path confirming the cached state (staleverdict's fix): without it
// the cache is never re-read, the peer write never flips the verdict and no
// delivery is ever accepted. Both fixes are in the same change.
//
// WorkflowsFastPath is pinned off because the janitor's empty-inbox probe
// invalidates the cache on its own and would mask the refusal, history
// signing because the test rewrites a history row, and clustered deployment
// because the topology is a single host.
type acceptedafterwindow struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
	logline  *logline.LogLine
}

func (a *acceptedafterwindow) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	a.store = fault.New(t)
	sock := socket.New(t)
	a.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(a.store),
	)
	component := fmt.Sprintf(`
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
`, a.ss.SocketName())
	a.logline = logline.New(t, logline.WithCaptureAll())

	a.workflow = workflow.New(t,
		workflow.WithFastPath(false),
		workflow.WithSigning(false),
		workflow.WithClusteredDeployment(false),
		workflow.WithNoDB(),
		workflow.WithDaprdOptions(0,
			daprd.WithSocket(t, sock),
			daprd.WithResourceFiles(component),
			daprd.WithExecOptions(
				exec.WithStdout(a.logline.Stdout()), exec.WithStderr(a.logline.Stderr()),
				exec.WithEnvVars(t, "DAPR_WORKFLOW_TEST_ACTIVITY_PUBLISH_RETRY_WINDOW", "2s"),
			),
		),
	)

	return []framework.Option{
		framework.WithProcesses(a.logline, a.ss, a.workflow),
	}
}

func (a *acceptedafterwindow) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	started := make(chan struct{})
	release := make(chan struct{})
	markStarted := sync.OnceFunc(func() { close(started) })
	releaseOnce := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseOnce)

	reg := a.workflow.Registry()
	require.NoError(t, reg.AddWorkflowN("acceptedafterwindow", func(wctx *task.WorkflowContext) (any, error) {
		var out string
		if err := wctx.CallActivity("gated").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	// Re-entrant: the contract is at-least-once, so a re-execution of the
	// body must not panic.
	require.NoError(t, reg.AddActivityN("gated", func(task.ActivityContext) (any, error) {
		markStarted()
		<-release
		return "done", nil
	}))

	const id = "acceptedafterwindow"
	rows := fworkflow.ComponentRows(a.store, fworkflow.WorkflowActorKeyPrefix(a.workflow.Dapr(), id))

	client := a.workflow.BackendClient(t, ctx)
	_, err := client.ScheduleNewWorkflow(ctx, "acceptedafterwindow", api.WithInstanceID(id))
	require.NoError(t, err)

	select {
	case <-started:
	case <-time.After(time.Second * 20):
		require.Fail(t, "timed out waiting for the activity to start")
	}

	// A turn dispatches its activities before it saves, so task 0's
	// scheduling is durable only once its row is readable.
	var execReal string
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, ev := fworkflow.TaskScheduledRow(t, ctx, rows, 0)
		if !assert.NotNil(c, ev, "task 0 must be recorded in history") {
			return
		}
		execReal = ev.GetTaskScheduled().GetTaskExecutionId()
		assert.NotEmpty(c, execReal, "task 0 must be recorded with an execution id")
	}, time.Second*20, time.Millisecond*10,
		"the scheduling of task 0 must be durable before it is rewritten")

	historyLen := fworkflow.HistoryCount(t, ctx, rows)
	fworkflow.SetTaskExecutionID(t, ctx, rows, 0, "superseded-"+execReal)

	// The actor's next save from its warm cache is refused on the moved
	// ETag, which drops the cache, and the retry loads the peer's rows.
	// Raising an event is the cheapest way to make the actor write.
	require.Eventually(t, func() bool {
		return client.RaiseEvent(ctx, id, "reload") == nil
	}, time.Second*20, time.Millisecond*100,
		"the actor must take the peer's rows before the event is accepted")
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Greater(c, fworkflow.HistoryCount(t, ctx, rows), historyLen)
	}, time.Second*20, time.Millisecond*10, "the reload event's turn must commit")

	// The result is now refused as superseded, and the sender keeps it in
	// hand: from here the retry loop has a refusal to remember.
	releaseOnce()
	retried := fmt.Sprintf("result publish for workflow '%s' refused, retrying with the result in hand", id)
	require.Eventually(t, func() bool { return a.logline.Contains(retried) },
		time.Second*20, time.Millisecond*10,
		"the superseded cache must refuse the result at least once")

	// The next inbox commit is parked inside the store, holding the actor
	// lock, so the window expires while the admission is in flight.
	arrived, releaseHold := a.store.ArmMultiHold("||inbox-")
	t.Cleanup(releaseHold)

	// The peer puts the recorded scheduling back: the next retry is judged
	// on a fresh load and admitted.
	fworkflow.SetTaskExecutionID(t, ctx, rows, 0, execReal)

	select {
	case <-arrived:
	case <-time.After(time.Second * 20):
		require.Fail(t, "the admitted result's inbox commit must be captured")
	}
	// Longer than the sender's window, which is the whole point: the call
	// that committed this row returns after its deadline has passed.
	time.Sleep(time.Second * 3)
	releaseHold()

	inboxAdd := fmt.Sprintf("Workflow actor '%s': adding event to the workflow inbox", id)
	require.True(t, a.logline.EventuallyContains(t, inboxAdd, time.Second*20, time.Millisecond*10),
		"the orchestrator must have accepted the delivery")

	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err, "a committed result must reach the workflow")
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus(), "%v", meta.GetFailureDetails())
	assert.Equal(t, `"done"`, meta.GetOutput().GetValue())
	// Nothing durable may be left behind once the result is consumed.
	fworkflow.WaitNoEventWakeups(t, ctx, a.workflow)

	// The sender's verdict on the held call is logged when that call
	// returns, which is milliseconds after the hold is lifted and races the
	// turn the commit drove: settle before reading the absence.
	time.Sleep(time.Second)
	assert.False(t, a.logline.Contains(
		fmt.Sprintf("dropping the result for workflow '%s', still superseded after the retry window", id)),
		"an accepted delivery must not be reported to its sender as the remembered refusal")
}
