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

package fallback

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(crossappescalation))
}

// crossappescalation runs an activity on app Y for a workflow on app X under
// WorkflowsFastPath. Y's worker disconnects while the activity runs, so the
// local drive on Y escalates to the run-activity reminder. Y's state store
// has no row for X's workflow, which must not read as a purge: the reminder
// is not dropped, and the activity runs once a worker connects to Y again.
type crossappescalation struct {
	workflow *workflow.Workflow
}

func (c *crossappescalation) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithFastPath(true),
		workflow.WithStagedOperations(true),
		workflow.WithFallbackSettleN(t, 0),
		workflow.WithFallbackSettleN(t, 1),
	)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *crossappescalation) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	appY := c.workflow.DaprN(1).AppID()

	require.NoError(t, c.workflow.RegistryN(0).AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		err := ctx.CallActivity("act", task.WithActivityAppID(appY)).Await(&out)
		return out, err
	}))
	var runs atomic.Int32
	require.NoError(t, c.workflow.RegistryN(1).AddActivityN("act", func(actx task.ActivityContext) (any, error) {
		if runs.Add(1) == 1 {
			<-actx.Context().Done()
			return nil, actx.Context().Err()
		}
		return "done", nil
	}))

	c.workflow.ConnectWorkerN(t, ctx, 0, c.workflow.RegistryN(0))
	c.workflow.WaitForConnectedWorkersN(t, ctx, 0, 1)
	workerY := c.workflow.ConnectWorkerN(t, ctx, 1, c.workflow.RegistryN(1))
	c.workflow.WaitForConnectedWorkersN(t, ctx, 1, 1)

	mgmt := c.workflow.ManagementClientN(t, ctx, 0)
	id, err := mgmt.ScheduleNewWorkflow(ctx, "parent", api.WithInstanceID("crossappescalation"))
	require.NoError(t, err)
	require.Eventually(t, func() bool { return runs.Load() == 1 }, time.Second*10, time.Millisecond*10)

	const name = "dapr_runtime_workflow_local_activity_count"
	workerY.Disconnect(t)
	c.workflow.WaitForNoConnectedWorkersN(t, ctx, 1)
	require.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.Positive(col, c.workflow.DaprN(1).Metrics(col, ctx).SumWithLabels(name, "status:escalated"))
	}, time.Second*10, time.Millisecond*10, "the activity drive on Y did not escalate")

	c.workflow.ConnectWorkerN(t, ctx, 1, c.workflow.RegistryN(1))
	meta, err := mgmt.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.Equal(t, `"done"`, meta.GetOutput().GetValue())
	assert.Equal(t, int32(2), runs.Load())
	assert.Zero(t, c.workflow.DaprN(1).Metrics(t, ctx).SumWithLabels(name, "status:purged_dropped"),
		"Y read the live workflow of X as purged")
}
