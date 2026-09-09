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
	"time"

	"github.com/stretchr/testify/assert"
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
	suite.Register(new(executoridle))
}

// executoridle runs a burst of clustered work items and requires every
// executor rendezvous actor to be deactivated once the work is done.
type executoridle struct {
	workflow *workflow.Workflow
}

func (e *executoridle) Setup(t *testing.T) []framework.Option {
	e.workflow = workflow.NewClustered(t, 3)

	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *executoridle) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	const (
		daprds    = 3
		instances = 150
		fanWidth  = 4
	)

	for i := range daprds {
		reg := e.workflow.RegistryN(i)
		require.NoError(t, reg.AddWorkflowN("burst", func(ctx *task.WorkflowContext) (any, error) {
			tasks := make([]task.Task, fanWidth)
			for j := range fanWidth {
				tasks[j] = ctx.CallActivity("noop", task.WithActivityInput(j))
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
	}

	clients := make([]*client.TaskHubGrpcClient, daprds)
	for i := range daprds {
		clients[i] = e.workflow.BackendClientN(t, ctx, i)
	}

	ids := make([]api.InstanceID, instances)
	for i := range instances {
		ids[i] = api.InstanceID(fmt.Sprintf("burst-%d", i))
		_, err := clients[i%daprds].ScheduleNewWorkflow(ctx, "burst", api.WithInstanceID(ids[i]))
		require.NoError(t, err)
	}

	fworkflow.WaitForAllCompleted(t, ctx, clients[0], ids...)

	d := e.workflow.Dapr()
	executorType := fmt.Sprintf("dapr.internal.%s.%s.executor", d.Namespace(), d.AppID())
	for i := range daprds {
		assert.EventuallyWithT(t, func(col *assert.CollectT) {
			count, hosted := e.workflow.DaprN(i).ActiveActorCount(col, ctx, executorType)
			assert.True(col, hosted, "executor actor type not hosted")
			assert.Zero(col, count, "executor actors left active")
		}, 30*time.Second, 10*time.Millisecond)
	}
}
