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

package keptinbox

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(idle))
}

// idle asserts that the janitor runs no turn while the inbox holds
// only an early result, and that the result still resolves its step once the
// workflow schedules it.
type idle struct {
	workflow *workflow.Workflow
}

func (k *idle) Setup(t *testing.T) []framework.Option {
	k.workflow = workflow.New(t,
		workflow.WithFastPath(true),
		// The injected unsigned result would be rejected by signing verification.
		workflow.WithSigning(false),
		workflow.WithDaprdOptions(0, daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_WORKFLOW_JANITOR_PERIOD", "2s",
		))),
	)
	return []framework.Option{
		framework.WithProcesses(k.workflow),
	}
}

func (k *idle) Run(t *testing.T, ctx context.Context) {
	k.workflow.WaitUntilRunning(t, ctx)

	var slowRuns, stepRuns atomic.Int32
	release := make(chan struct{})

	// The WaitForSingleEvent synthetic timer takes id 0, Slow id 1, Step id 2.
	require.NoError(t, k.workflow.Registry().AddWorkflowN("keptinboxidle", func(c *task.WorkflowContext) (any, error) {
		if err := c.WaitForSingleEvent("go", time.Hour).Await(nil); err != nil {
			return nil, err
		}
		if err := c.CallActivity("Slow").Await(nil); err != nil {
			return nil, err
		}
		var out string
		if err := c.CallActivity("Step").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, k.workflow.Registry().AddActivityN("Slow", func(task.ActivityContext) (any, error) {
		slowRuns.Add(1)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil, nil
	}))
	require.NoError(t, k.workflow.Registry().AddActivityN("Step", func(task.ActivityContext) (any, error) {
		stepRuns.Add(1)
		return "ran", nil
	}))

	cl := k.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "keptinboxidle")
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 1, fworkflow.CountHistoryEventsMatching(t, ctx, cl, id, fworkflow.IsTimerCreatedFor(0)))
	}, 10*time.Second, 10*time.Millisecond)

	// Step's result arrives before Step is scheduled, so every turn until
	// then keeps it in the inbox.
	fworkflow.InjectInboxEvent(t, ctx, k.workflow.DB(), k.workflow.Dapr(), string(id), fworkflow.TaskCompletedEvent(2, `"planted"`))
	k.workflow.Dapr().Restart(t, ctx)
	k.workflow.Dapr().WaitUntilRunning(t, ctx)
	cl = k.workflow.BackendClient(t, ctx)

	require.NoError(t, cl.RaiseEvent(ctx, id, "go"))
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int32(1), slowRuns.Load())
	}, 10*time.Second, 10*time.Millisecond)

	metric := func(status string) float64 {
		return k.workflow.Dapr().Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_wake_count", "status:"+status)
	}
	recovered := metric("janitor_recovered")

	// While Slow runs, two janitor fires take the idle path: the first
	// re-dispatches Slow, the second escalates it.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, k.workflow.Dapr().Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_activity_count", "status:janitor_redispatch_escalated"), float64(1))
	}, 20*time.Second, 10*time.Millisecond, "the janitor never reached the re-dispatch while the inbox held only an early result")
	assert.InDelta(t, recovered, metric("janitor_recovered"), 0, "the janitor ran a turn over an inbox holding only an early result")

	close(release)

	wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	meta, err := cl.WaitForWorkflowCompletion(wctx, id)
	require.NoError(t, err)
	require.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.Equal(t, `"planted"`, meta.GetOutput().GetValue(), "the held result must resolve Step once Step is scheduled")
	assert.Equal(t, int32(1), slowRuns.Load())
	assert.Zero(t, stepRuns.Load())
}
