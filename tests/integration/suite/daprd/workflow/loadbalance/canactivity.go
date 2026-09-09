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
	suite.Register(new(canactivity))
}

// canactivity runs ContinueAsNew loops with one activity per generation across
// a clustered deployment.
type canactivity struct {
	workflow *workflow.Workflow
}

func (c *canactivity) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.NewClustered(t, 3)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *canactivity) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	const (
		daprds      = 3
		generations = 5
		instances   = 50
	)

	for i := range daprds {
		reg := c.workflow.RegistryN(i)
		require.NoError(t, reg.AddWorkflowN("can-activity", func(ctx *task.WorkflowContext) (any, error) {
			var gen int
			if err := ctx.GetInput(&gen); err != nil {
				return nil, err
			}
			if err := ctx.CallActivity("noop", task.WithActivityInput(gen)).Await(nil); err != nil {
				return nil, err
			}
			if gen < generations {
				ctx.ContinueAsNew(gen + 1)
			}
			return nil, nil
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
		ids[i] = api.InstanceID(fmt.Sprintf("can-activity-%d", i))
		_, err := clients[i%daprds].ScheduleNewWorkflow(ctx, "can-activity",
			api.WithInstanceID(ids[i]),
			api.WithInput(0),
		)
		require.NoError(t, err)
	}

	fworkflow.WaitForAllCompleted(t, ctx, clients[0], ids...)
}
