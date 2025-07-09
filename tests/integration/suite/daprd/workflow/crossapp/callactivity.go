/*
Copyright 2025 The Dapr Authors
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

package crossapp

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(callactivity))
}

// callactivity demonstrates calling activities across different Dapr applications.
type callactivity struct {
	workflow *workflow.Workflow
}

func (c *callactivity) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t,
		workflow.WithDaprds(2))

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *callactivity) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	// Add orchestrator to app0's registry
	c.workflow.Registry(0).AddOrchestratorN("CrossAppWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app0: %w", err)
		}
		var output string

		err := ctx.CallActivity("ProcessData",
			task.WithActivityInput(input),
			task.WithAppID(c.workflow.DaprN(1).AppID())). // app1
			Await(&output)
		if err != nil {
			return nil, fmt.Errorf("failed to execute activity in app1: %w", err)
		}
		return output, nil
	})

	// Add activity to app1's registry
	c.workflow.Registry(1).AddActivityN("ProcessData", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app1 activity: %w", err)
		}
		return fmt.Sprintf("Processed by app1: %s", input), nil
	})

	// Start workflow listeners for each app with their respective registries
	client0 := c.workflow.BackendClient(t, ctx, 0) // app0 (orchestrator)
	c.workflow.BackendClient(t, ctx, 1)            // app1 (activity)

	id, err := client0.ScheduleNewOrchestration(ctx, "CrossAppWorkflow", api.WithInput("Hello from app0"))
	require.NoError(t, err)

	metadata, err := client0.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.OrchestrationMetadataIsComplete(metadata))
	assert.Equal(t, `"Processed by app1: Hello from app0"`, metadata.GetOutput().GetValue())
}
