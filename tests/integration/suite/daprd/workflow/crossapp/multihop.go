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

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(multihop))
}

// multihop demonstrates multi-hop cross-app workflow: app0 → app1 → app2
type multihop struct {
	workflow *workflow.Workflow
}

func (m *multihop) Setup(t *testing.T) []framework.Option {
	m.workflow = workflow.New(t,
		workflow.WithDaprds(3),
	)

	return []framework.Option{
		framework.WithProcesses(m.workflow),
	}
}

func (m *multihop) Run(t *testing.T, ctx context.Context) {
	m.workflow.WaitUntilRunning(t, ctx)

	// App1: 1st hop - processes data
	m.workflow.RegistryN(1).AddActivityN("ProcessData", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app1: %w", err)
		}
		return fmt.Sprintf("Processed by app1: %s", input), nil
	})

	// App2: 2nd hop - transforms data
	m.workflow.RegistryN(2).AddActivityN("TransformData", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app2: %w", err)
		}
		return fmt.Sprintf("Transformed by app2: %s", input), nil
	})

	// App0: Orchestrator - coordinates the multi-hop workflow
	m.workflow.Registry().AddOrchestratorN("MultiHopWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in orchestrator: %w", err)
		}

		// Step 1: Call app1 to process data
		var result1 string
		err := ctx.CallActivity("ProcessData",
			task.WithActivityInput(input),
			task.WithAppID(m.workflow.DaprN(1).AppID())).
			Await(&result1)
		if err != nil {
			return nil, fmt.Errorf("failed to execute activity in app1: %w", err)
		}

		// Step 2: Call app2 to transform the processed data
		var result2 string
		err = ctx.CallActivity("TransformData",
			task.WithActivityInput(result1),
			task.WithAppID(m.workflow.DaprN(2).AppID())).
			Await(&result2)
		if err != nil {
			return nil, fmt.Errorf("failed to execute activity in app2: %w", err)
		}

		return result2, nil
	})

	// Start workflow listeners for each app
	client0 := m.workflow.BackendClient(t, ctx) // app0 (orchestrator)
	m.workflow.BackendClientN(t, ctx, 1)        // app1 (activity)
	m.workflow.BackendClientN(t, ctx, 2)        // app2 (activity)

	// Start the multi-hop workflow
	id, err := client0.ScheduleNewOrchestration(ctx, "MultiHopWorkflow", api.WithInput("Hello from app0"))
	assert.NoError(t, err)

	metadata, err := client0.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
	assert.NoError(t, err)

	assert.True(t, api.OrchestrationMetadataIsComplete(metadata))
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, metadata.RuntimeStatus)

	// Verify the final result shows the complete chain
	expectedResult := `"Transformed by app2: Processed by app1: Hello from app0"`
	assert.Equal(t, expectedResult, metadata.GetOutput().GetValue())
}
