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
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(staleverdict))
}

// staleverdict pins the re-read of the cache a scheduling refusal was judged
// on. A superseded verdict is computed from the history the workflow actor
// holds in memory, and the sender retries that refusal with the result in
// hand for its whole window: a cache a peer has already moved past would
// have every one of those retries judged against the same frozen view, and
// the result dropped at the end of it. The refusal path confirms the cached
// state, so a metadata row whose ETag has moved drops the cache and the next
// retry is judged on a fresh load.
//
// The construct moves the durable rows underneath a warm cache, which is
// what a host lagging a peer sees and what confirmCachedState was written
// for. The literal lagging-replica trigger (an activity orphaned by a
// ContinueAsNew whose result names the previous scheduling) is NOT
// constructible on a single host: the sidecar's pending-activity
// registration is keyed by instance and task id, so the new generation's
// dispatch of the same task id evicts the orphan's registration and its
// response is discarded as stale before it ever reaches the activity actor's
// publish path. That shape therefore needs two sidecars, which is
// loadbalance/canstragglerdone; here the same verdict is produced from one
// host by rewriting task 0's recorded execution id:
//
//  1. the activity is dispatched and held, so its result will name the
//     execution id recorded at dispatch;
//  2. a peer rewrites that recorded id and moves the metadata ETag, so the
//     actor's next save is refused and its cache is dropped;
//  3. the actor reloads and now holds a scheduling the result does not name;
//  4. the peer puts the recorded id back and moves the ETag again, so the
//     store agrees with the result while the cache still refuses it.
//
// WorkflowsFastPath is pinned off because the janitor's empty-inbox probe
// invalidates the cache on its own and would mask the bug, history signing
// because the test rewrites a history row, and clustered deployment because
// the topology is a single host.
type staleverdict struct {
	workflow *workflow.Workflow
	logline  *logline.LogLine
}

func (s *staleverdict) Setup(t *testing.T) []framework.Option {
	s.logline = logline.New(t, logline.WithCaptureAll())
	s.workflow = workflow.New(t,
		workflow.WithFastPath(false),
		workflow.WithSigning(false),
		workflow.WithClusteredDeployment(false),
		workflow.WithDaprdOptions(0, daprd.WithExecOptions(
			exec.WithStdout(s.logline.Stdout()), exec.WithStderr(s.logline.Stderr()),
			exec.WithEnvVars(t, "DAPR_WORKFLOW_TEST_ACTIVITY_PUBLISH_RETRY_WINDOW", "2s"),
		)),
	)

	return []framework.Option{
		framework.WithProcesses(s.logline, s.workflow),
	}
}

func (s *staleverdict) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	started := make(chan struct{})
	release := make(chan struct{})
	markStarted := sync.OnceFunc(func() { close(started) })
	releaseOnce := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseOnce)

	reg := s.workflow.Registry()
	require.NoError(t, reg.AddWorkflowN("staleverdict", func(wctx *task.WorkflowContext) (any, error) {
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

	client := s.workflow.BackendClient(t, ctx)
	id, err := client.ScheduleNewWorkflow(ctx, "staleverdict", api.WithInstanceID("staleverdict"))
	require.NoError(t, err)

	select {
	case <-started:
	case <-time.After(time.Second * 20):
		require.Fail(t, "timed out waiting for the activity to start")
	}

	// A turn dispatches its activities before it saves, so task 0's
	// scheduling is durable only once its row is readable.
	rows := fworkflow.SQLiteRows(s.workflow.DB(), string(id))
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

	// A peer moves the instance on: task 0's recorded scheduling names an
	// execution the held result does not, and the metadata ETag moves with
	// it.
	historyLen := fworkflow.HistoryCount(t, ctx, rows)
	fworkflow.SetTaskExecutionID(t, ctx, rows, 0, "superseded-"+execReal)

	// The actor's next save from its warm cache is refused on that ETag,
	// which drops the cache, and the retry loads the peer's rows. Raising an
	// event is the cheapest way to make the actor write.
	require.Eventually(t, func() bool {
		return client.RaiseEvent(ctx, id, "reload") == nil
	}, time.Second*20, time.Millisecond*100,
		"the actor must take the peer's rows before the event is accepted")
	require.True(t, s.logline.EventuallyContains(t,
		"save aborted by peer write (etag mismatch); surfacing for retry",
		time.Second*20, time.Millisecond*10),
		"the peer write must abort the actor's save so its cache is dropped")

	// The turn that event drove has committed once the event is durable, and
	// nothing writes this instance's rows after it.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Greater(c, fworkflow.HistoryCount(t, ctx, rows), historyLen)
	}, time.Second*20, time.Millisecond*10, "the reload event's turn must commit")

	// The peer puts the recorded scheduling back: the store agrees with the
	// held result again while the cache still holds the superseded view.
	fworkflow.SetTaskExecutionID(t, ctx, rows, 0, execReal)

	releaseOnce()

	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err,
		"the result must be admitted once the refusal is re-judged on the store's rows")
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus(), "%v", meta.GetFailureDetails())
	assert.Equal(t, `"done"`, meta.GetOutput().GetValue())
	// The stale cache must really have refused the result: without a refusal
	// there is nothing for the re-read to correct.
	assert.True(t, s.logline.EventuallyContains(t,
		fmt.Sprintf("result publish for workflow '%s' refused, retrying with the result in hand", id),
		time.Second*20, time.Millisecond*10),
		"the superseded cache must refuse the result at least once")
	assert.False(t, s.logline.Contains(
		fmt.Sprintf("dropping the result for workflow '%s', still superseded after the retry window", id)),
		"a verdict the store has moved past must not be spent on the whole window and dropped")
}
