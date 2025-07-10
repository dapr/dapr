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
	suite.Register(new(manyapps))
}

// manyapps demonstrates complex cross-app workflows with many apps
type manyapps struct {
	workflow *workflow.Workflow
}

func (m *manyapps) Setup(t *testing.T) []framework.Option {
	m.workflow = workflow.New(t,
		workflow.WithDaprds(5))

	// App1: Data processing
	m.workflow.RegistryN(1).AddActivityN("ProcessData", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app1: %w", err)
		}
		return "Processed by app1: " + input, nil
	})

	// App2: Data validation
	m.workflow.RegistryN(2).AddActivityN("ValidateData", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app2: %w", err)
		}
		return "Validated by app2: " + input, nil
	})

	// App3: Data transformation
	m.workflow.RegistryN(3).AddActivityN("TransformData", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app3: %w", err)
		}
		return "Transformed by app3: " + input, nil
	})

	// App4: Data enrichment
	m.workflow.RegistryN(4).AddActivityN("EnrichData", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app4: %w", err)
		}
		return "Enriched by app4: " + input, nil
	})

	// App0: Orchestrator that coordinates all apps
	err := m.workflow.Registry().AddOrchestratorN("ManyAppsWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in orchestrator: %w", err)
		}

		// Step 1: Process data in app1
		var result1 string
		err := ctx.CallActivity("ProcessData",
			task.WithActivityInput(input),
			task.WithAppID(m.workflow.DaprN(1).AppID())).
			Await(&result1)
		if err != nil {
			return nil, fmt.Errorf("failed to execute ProcessData: %w", err)
		}

		// Step 2: Validate data in app2
		var result2 string
		err = ctx.CallActivity("ValidateData",
			task.WithActivityInput(result1),
			task.WithAppID(m.workflow.DaprN(2).AppID())).
			Await(&result2)
		if err != nil {
			return nil, fmt.Errorf("failed to execute ValidateData: %w", err)
		}

		// Step 3: Transform data in app3
		var result3 string
		err = ctx.CallActivity("TransformData",
			task.WithActivityInput(result2),
			task.WithAppID(m.workflow.DaprN(3).AppID())).
			Await(&result3)
		if err != nil {
			return nil, fmt.Errorf("failed to execute TransformData: %w", err)
		}

		// Step 4: Enrich data in app4
		var result4 string
		err = ctx.CallActivity("EnrichData",
			task.WithActivityInput(result3),
			task.WithAppID(m.workflow.DaprN(4).AppID())).
			Await(&result4)
		if err != nil {
			return nil, fmt.Errorf("failed to execute EnrichData: %w", err)
		}

		return result4, nil
	})
	require.NoError(t, err)

	return []framework.Option{
		framework.WithProcesses(m.workflow),
	}
}

func (m *manyapps) Run(t *testing.T, ctx context.Context) {
	m.workflow.WaitUntilRunning(t, ctx)

	// Start workflow listeners for each app with their respective registries
	client0 := m.workflow.BackendClient(t, ctx) // app0 (orchestrator)
	m.workflow.BackendClientN(t, ctx, 1)        // app1 (ProcessData)
	m.workflow.BackendClientN(t, ctx, 2)        // app2 (ValidateData)
	m.workflow.BackendClientN(t, ctx, 3)        // app3 (TransformData)
	m.workflow.BackendClientN(t, ctx, 4)        // app4 (EnrichData)

	id, err := client0.ScheduleNewOrchestration(ctx, "ManyAppsWorkflow", api.WithInput("Hello from app0"))
	require.NoError(t, err)
	metadata, err := client0.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.OrchestrationMetadataIsComplete(metadata))
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, metadata.RuntimeStatus)
	expectedResult := `"Enriched by app4: Transformed by app3: Validated by app2: Processed by app1: Hello from app0"`
	assert.Equal(t, expectedResult, metadata.GetOutput().GetValue())
}
