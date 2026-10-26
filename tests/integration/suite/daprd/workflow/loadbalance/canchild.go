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

package loadbalance

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(canchild))
}

// canchild runs ContinueAsNew loops that create a child workflow with an
// activity per generation across a clustered deployment.
type canchild struct {
	workflow *workflow.Workflow
}

func (c *canchild) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.NewClustered(t, 3)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *canchild) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	const (
		daprds      = 3
		generations = 4
		instances   = 30
	)

	for i := range daprds {
		reg := c.workflow.RegistryN(i)
		require.NoError(t, reg.AddWorkflowN("can-parent", func(ctx *task.WorkflowContext) (any, error) {
			var gen int
			if err := ctx.GetInput(&gen); err != nil {
				return nil, err
			}
			var out int
			err := ctx.CallChildWorkflow("can-child",
				task.WithChildWorkflowInput(gen),
				task.WithChildWorkflowInstanceID(fmt.Sprintf("%s-child-%d", ctx.ID, gen)),
			).Await(&out)
			if err != nil {
				return nil, err
			}
			if out != gen {
				return nil, fmt.Errorf("child of generation %d returned %d", gen, out)
			}
			if gen < generations {
				ctx.ContinueAsNew(gen + 1)
			}
			return nil, nil
		}))
		require.NoError(t, reg.AddWorkflowN("can-child", func(ctx *task.WorkflowContext) (any, error) {
			var gen int
			if err := ctx.GetInput(&gen); err != nil {
				return nil, err
			}
			if err := ctx.CallActivity("noop", task.WithActivityInput(gen)).Await(nil); err != nil {
				return nil, err
			}
			return gen, nil
		}))
		require.NoError(t, reg.AddActivityN("noop", func(task.ActivityContext) (any, error) {
			return nil, nil
		}))
	}

	clients := make([]*client.TaskHubGrpcClient, daprds)
	for i := range daprds {
		clients[i] = c.workflow.BackendClientN(t, ctx, i)
	}

	ids := make([]api.InstanceID, instances)
	for i := range instances {
		ids[i] = api.InstanceID(fmt.Sprintf("can-child-%d", i))
		_, err := clients[i%daprds].ScheduleNewWorkflow(ctx, "can-parent",
			api.WithInstanceID(ids[i]),
			api.WithInput(0),
		)
		require.NoError(t, err)
	}

	fworkflow.WaitForAllCompleted(t, ctx, clients[0], ids...)
}
