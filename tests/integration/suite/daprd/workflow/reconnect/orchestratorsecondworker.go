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

package reconnect

import (
	"context"
	"fmt"
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
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(orchestratorsecondworker))
}

// orchestratorsecondworker disconnects the worker whose connect registered
// the workflow actor types while a second worker stays connected, so the
// types stay registered. The idle reaper must still deactivate the workflow
// actors of the workflows the second worker then completes.
type orchestratorsecondworker struct {
	workflow *workflow.Workflow
}

func (o *orchestratorsecondworker) Setup(t *testing.T) []framework.Option {
	o.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_WORKFLOW_REAPER_SCAN_INTERVAL", "100ms",
			"DAPR_WORKFLOW_REAPER_IDLE_TTL", "1s",
		))),
	)

	return []framework.Option{
		framework.WithProcesses(o.workflow),
	}
}

func (o *orchestratorsecondworker) Run(t *testing.T, ctx context.Context) {
	o.workflow.WaitUntilRunning(t, ctx)

	reg := o.workflow.Registry()
	require.NoError(t, reg.AddWorkflowN("single", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity("noop").Await(nil)
	}))
	require.NoError(t, reg.AddActivityN("noop", func(task.ActivityContext) (any, error) {
		return nil, nil
	}))

	d := o.workflow.Dapr()
	first := o.workflow.ConnectWorker(t, ctx, reg)
	o.workflow.WaitForConnectedWorkers(t, ctx, 1)
	o.workflow.ConnectWorker(t, ctx, reg)
	o.workflow.WaitForConnectedWorkers(t, ctx, 2)

	first.Disconnect(t)
	o.workflow.WaitForExactlyConnectedWorkers(t, ctx, 1)

	mgmt := o.workflow.ManagementClient(t, ctx)

	const instances = 10
	ids := make([]api.InstanceID, instances)
	for i := range instances {
		ids[i] = api.InstanceID(fmt.Sprintf("single-%d", i))
		_, err := mgmt.ScheduleNewWorkflow(ctx, "single", api.WithInstanceID(ids[i]))
		require.NoError(t, err)
	}
	fworkflow.WaitForAllCompleted(t, ctx, mgmt, ids...)

	workflowType := fmt.Sprintf("dapr.internal.%s.%s.workflow", d.Namespace(), d.AppID())
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		count, hosted := d.ActiveActorCount(c, ctx, workflowType)
		assert.True(c, hosted, "workflow actor type not hosted")
		assert.Zero(c, count, "idle workflow actors not reaped after the registering worker left")
	}, time.Second*30, time.Millisecond*100)
}
