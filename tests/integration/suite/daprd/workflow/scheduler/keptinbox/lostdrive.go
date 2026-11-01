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
	suite.Register(new(lostdrive))
}

// lostdrive asserts that an early result held in the inbox, waiting for its
// step to be scheduled, does not stop the janitor from re-dispatching an
// in-flight activity whose local drive was lost.
type lostdrive struct {
	workflow *workflow.Workflow
}

func (k *lostdrive) Setup(t *testing.T) []framework.Option {
	k.workflow = workflow.New(t,
		workflow.WithFastPath(true),
		// The injected unsigned result would be rejected by signing verification.
		workflow.WithSigning(false),
		workflow.WithDaprdOptions(0, daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_WORKFLOW_JANITOR_PERIOD", "2s",
			"DAPR_WORKFLOW_TEST_DROP_ACTIVITY_DRIVES", "1000",
		))),
	)
	return []framework.Option{
		framework.WithProcesses(k.workflow),
	}
}

func (k *lostdrive) Run(t *testing.T, ctx context.Context) {
	k.workflow.WaitUntilRunning(t, ctx)

	var executions atomic.Int32

	// The WaitForSingleEvent synthetic timer takes id 0, Echo takes id 1.
	require.NoError(t, k.workflow.Registry().AddWorkflowN("keptinbox", func(c *task.WorkflowContext) (any, error) {
		if err := c.WaitForSingleEvent("go", time.Hour).Await(nil); err != nil {
			return nil, err
		}
		var out string
		if err := c.CallActivity("Echo").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, k.workflow.Registry().AddActivityN("Echo", func(task.ActivityContext) (any, error) {
		executions.Add(1)
		return "ok", nil
	}))

	cl := k.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "keptinbox")
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 1, fworkflow.CountHistoryEventsMatching(t, ctx, cl, id, fworkflow.IsTimerCreatedFor(0)))
	}, 10*time.Second, 10*time.Millisecond)

	// Task 50 is never scheduled, so every turn keeps its result in the inbox.
	fworkflow.InjectInboxEvent(t, ctx, k.workflow.DB(), k.workflow.Dapr(), string(id), fworkflow.TaskCompletedEvent(50, `"held"`))
	k.workflow.Dapr().Restart(t, ctx)
	k.workflow.Dapr().WaitUntilRunning(t, ctx)
	cl = k.workflow.BackendClient(t, ctx)

	// Echo's local drive is dropped, so only the janitor can re-dispatch it.
	require.NoError(t, cl.RaiseEvent(ctx, id, "go"))

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err, "the held result stopped the janitor from re-dispatching the lost activity")
	require.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.Equal(t, `"ok"`, meta.GetOutput().GetValue())
	assert.Equal(t, int32(1), executions.Load())
	assert.GreaterOrEqual(t, k.workflow.Dapr().Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_activity_count", "status:janitor_redispatch_escalated"), float64(1))
}
