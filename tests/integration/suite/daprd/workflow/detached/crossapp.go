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

package detached

import (
	"context"
	"testing"

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
	suite.Register(new(crossapp))
}

// crossapp asserts that a detached workflow can be spawned into a different
// app via WithDetachedWorkflowAppID. The spawned instance executes on the
// target app, and the parent terminates independently of the spawn.
type crossapp struct {
	workflow *workflow.Workflow
}

func (c *crossapp) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t,
		workflow.WithDaprds(2))
	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *crossapp) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	const spawnedInstanceID = "spawned-cross"

	// Caller registered on app0 only.
	c.workflow.Registry().AddWorkflowN("Caller", func(ctx *task.WorkflowContext) (any, error) {
		id, err := ctx.ScheduleNewWorkflow("RemoteSpawned",
			task.WithDetachedWorkflowInstanceID(spawnedInstanceID),
			task.WithDetachedWorkflowAppID(c.workflow.DaprN(1).AppID()),
			task.WithDetachedWorkflowInput("hello"),
		)
		if err != nil {
			return nil, err
		}
		return string(id), nil
	})

	// Spawn target registered on app1 only — proves the spawn was actually
	// routed cross-app rather than executed locally on app0.
	c.workflow.RegistryN(1).AddWorkflowN("RemoteSpawned", func(ctx *task.WorkflowContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return "", err
		}
		return "remote-saw:" + input, nil
	})

	app0Client := c.workflow.BackendClient(t, ctx)
	app1Client := c.workflow.BackendClientN(t, ctx, 1)

	parentID, err := app0Client.ScheduleNewWorkflow(ctx, "Caller")
	require.NoError(t, err)
	parentMeta, err := app0Client.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
	require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, parentMeta.GetRuntimeStatus())

	// Spawned must be reachable via app1 (its hosting daprd).
	spawnedMeta, err := app1Client.WaitForWorkflowCompletion(ctx, api.InstanceID(spawnedInstanceID),
		api.WithFetchPayloads(true))
	require.NoError(t, err)
	require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, spawnedMeta.GetRuntimeStatus())
	assert.Equal(t, `"remote-saw:hello"`, spawnedMeta.GetOutput().GetValue())
}
