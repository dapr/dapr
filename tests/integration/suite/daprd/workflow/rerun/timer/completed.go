/*
Copyright 2025 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://wwb.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package timer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(completed))
}

type completed struct {
	workflow *workflow.Workflow
}

func (c *completed) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *completed) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	c.workflow.Registry().AddOrchestratorN("completed-timer", func(ctx *task.OrchestrationContext) (any, error) {
		require.NoError(t, ctx.CreateTimer(time.Second).Await(nil))
		require.NoError(t, ctx.CallActivity("bar").Await(nil))
		return nil, nil
	})
	c.workflow.Registry().AddActivityN("bar", func(ctx task.ActivityContext) (any, error) {
		time.Sleep(time.Second)
		return nil, nil
	})

	client := c.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewOrchestration(ctx, "completed-timer", api.WithInstanceID("ijk"))
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)

	newID, err := client.RerunWorkflowFromEvent(ctx, id, 0)
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationCompletion(ctx, newID)
	require.NoError(t, err)

	newID, err = client.RerunWorkflowFromEvent(ctx, id, 1)
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationCompletion(ctx, newID)
	require.NoError(t, err)
}
