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

package retries

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(rejectedturn))
}

// rejectedturn runs a workflow that schedules its compensation in a Go defer.
// The SDK ends a blocked turn with a panic, so the defer runs on every turn
// that blocks. A later turn then creates Undo again under the task ID that the
// history already holds, and daprd rejects that turn on every try. The retry
// of a rejected turn must slow down: with the first hold set to 2s, no turn is
// rejected a second time within 1.5s of the first rejection, while the
// interval the scheduler waits between retries alone is always under 2s.
type rejectedturn struct {
	workflow *workflow.Workflow
	logline  *logline.LogLine
}

// rejections returns how many turns daprd has rejected so far.
func (r *rejectedturn) rejections(t assert.TestingT, ctx context.Context) int {
	return int(r.workflow.Dapr().Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_wake_count", "status:stale_turn_rejected"))
}

func (r *rejectedturn) Setup(t *testing.T) []framework.Option {
	r.logline = logline.New(t, logline.WithCaptureAll())
	r.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0,
			daprd.WithExecOptions(
				exec.WithStdout(r.logline.Stdout()),
				exec.WithStderr(r.logline.Stderr()),
				exec.WithEnvVars(t, "DAPR_WORKFLOW_TEST_REJECTED_TURN_BACKOFF_BASE", "2s"),
			),
		),
	)

	return []framework.Option{
		framework.WithProcesses(r.logline, r.workflow),
	}
}

func (r *rejectedturn) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	reg := r.workflow.Registry()
	require.NoError(t, reg.AddWorkflowN("DeferSaga", func(wctx *task.WorkflowContext) (any, error) {
		defer func() {
			_ = wctx.CallActivity("Undo")
		}()
		if err := wctx.CallActivity("Step").Await(nil); err != nil {
			return nil, err
		}
		if err := wctx.CallActivity("Step").Await(nil); err != nil {
			return nil, err
		}
		return "done", nil
	}))
	for _, name := range []string{"Step", "Undo"} {
		require.NoError(t, reg.AddActivityN(name, func(task.ActivityContext) (any, error) { return "ok", nil }))
	}

	client := r.workflow.BackendClient(t, ctx)
	const id = "saga-1"
	_, err := client.ScheduleNewWorkflow(ctx, "DeferSaga", api.WithInstanceID(id))
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Positive(c, r.rejections(c, ctx))
	}, time.Second*10, time.Millisecond*5, "the turn must be rejected")

	// The two activity results each drive a turn, and both are rejected at
	// once. Let the second land, then count from there.
	time.Sleep(time.Millisecond * 100)
	first := r.rejections(t, ctx)
	assert.Never(t, func() bool {
		return r.rejections(t, ctx) > first
	}, time.Millisecond*1500, time.Millisecond*50, "a rejected turn must not run again within the hold")

	assert.True(t, r.logline.Contains("committed 'Undo', turn response 'Undo'"), "the log must name the clashing operations")
	assert.True(t, r.logline.Contains("not deterministic"), "the log must name the likely cause")
	assert.True(t, r.logline.Contains("retrying after at least 2s"), "the log must give the hold")

	meta, err := client.FetchWorkflowMetadata(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_RUNNING, meta.GetRuntimeStatus())
}
