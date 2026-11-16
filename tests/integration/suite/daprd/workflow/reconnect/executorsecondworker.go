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
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(executorsecondworker))
}

// executorsecondworker disconnects the worker whose connect registered the
// workflow actor types while a second worker stays connected, so the types
// stay registered. The executor actors created for the work the second
// worker then runs must still be deactivated once that work is done.
type executorsecondworker struct {
	workflow *workflow.Workflow
}

func (e *executorsecondworker) Setup(t *testing.T) []framework.Option {
	e.workflow = workflow.New(t, workflow.WithClusteredDeployment(true))

	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *executorsecondworker) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	reg := e.workflow.Registry()
	require.NoError(t, reg.AddWorkflowN("fan", func(ctx *task.WorkflowContext) (any, error) {
		tasks := make([]task.Task, 4)
		for i := range tasks {
			tasks[i] = ctx.CallActivity("noop", task.WithActivityInput(i))
		}
		for _, tk := range tasks {
			if err := tk.Await(nil); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}))
	require.NoError(t, reg.AddActivityN("noop", func(task.ActivityContext) (any, error) {
		return nil, nil
	}))

	d := e.workflow.Dapr()
	first := e.workflow.ConnectWorker(t, ctx, reg)
	e.workflow.WaitForConnectedWorkers(t, ctx, 1)
	e.workflow.ConnectWorker(t, ctx, reg)
	e.workflow.WaitForConnectedWorkers(t, ctx, 2)

	first.Disconnect(t)
	e.workflow.WaitForExactlyConnectedWorkers(t, ctx, 1)

	mgmt := e.workflow.ManagementClient(t, ctx)

	// Few enough that the deactivation queue (100 slots) cannot overflow and
	// drop requests: one executor left behind is already the failure.
	const instances = 10
	ids := make([]api.InstanceID, instances)
	for i := range instances {
		ids[i] = api.InstanceID(fmt.Sprintf("fan-%d", i))
		_, err := mgmt.ScheduleNewWorkflow(ctx, "fan", api.WithInstanceID(ids[i]))
		require.NoError(t, err)
	}
	fworkflow.WaitForAllCompleted(t, ctx, mgmt, ids...)

	executorType := fmt.Sprintf("dapr.internal.%s.%s.executor", d.Namespace(), d.AppID())
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		count, hosted := d.ActiveActorCount(c, ctx, executorType)
		assert.True(c, hosted, "executor actor type not hosted")
		assert.Zero(c, count, "executor actors left active after the registering worker left")
	}, time.Second*30, time.Millisecond*10)
}
