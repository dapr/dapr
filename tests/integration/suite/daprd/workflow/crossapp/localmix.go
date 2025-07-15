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
	suite.Register(new(localmix))
}

// localmix demonstrates mixing local and cross-app activity calls
type localmix struct {
	workflow *workflow.Workflow
}

func (l *localmix) Setup(t *testing.T) []framework.Option {
	l.workflow = workflow.New(t,
		workflow.WithDaprds(2),
	)

	return []framework.Option{
		framework.WithProcesses(l.workflow),
	}
}

func (l *localmix) Run(t *testing.T, ctx context.Context) {
	l.workflow.WaitUntilRunning(t, ctx)

	l.workflow.Registry().AddActivityN("LocalProcess1", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in local activity: %w", err)
		}
		return "Local processed: " + input, nil
	})

	l.workflow.RegistryN(1).AddActivityN("RemoteProcess2", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in remote activity: %w", err)
		}
		return "Remote processed: " + input, nil
	})

	l.workflow.Registry().AddActivityN("LocalProcess3", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in local activity: %w", err)
		}
		return "Local processed: " + input, nil
	})

	l.workflow.Registry().AddActivityN("LocalProcess4", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in local activity: %w", err)
		}
		return "Local processed: " + input, nil
	})

	// App0: Orchestrator - mixes local & cross-app calls
	l.workflow.Registry().AddOrchestratorN("MixedWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in orchestrator: %w", err)
		}

		// Step 1: Call local activity (no AppID specified)
		var step1Result string
		err := ctx.CallActivity("LocalProcess1",
			task.WithActivityInput(input)).
			Await(&step1Result)
		if err != nil {
			return nil, fmt.Errorf("failed to execute step 1 local activity: %w", err)
		}

		// Step 2: Call cross-app activity
		var step2Result string
		err = ctx.CallActivity("RemoteProcess2",
			task.WithActivityInput(step1Result),
			task.WithActivityAppID(l.workflow.DaprN(1).AppID())).
			Await(&step2Result)
		if err != nil {
			return nil, fmt.Errorf("failed to execute step 2 remote activity: %w", err)
		}

		// Step 3: Call another local activity (no AppID specified)
		var step3Result string
		err = ctx.CallActivity("LocalProcess3",
			task.WithActivityInput(step2Result)).
			Await(&step3Result)
		if err != nil {
			return nil, fmt.Errorf("failed to execute step 3 local activity: %w", err)
		}

		// Step 4: Call another local activity (with local AppID specified)
		var step4Result string
		err = ctx.CallActivity("LocalProcess4",
			task.WithActivityInput(step3Result),
			task.WithActivityAppID(l.workflow.DaprN(0).AppID())).
			Await(&step4Result)
		if err != nil {
			return nil, fmt.Errorf("failed to execute step 4 local activity: %w", err)
		}

		return step4Result, nil
	})

	// Start workflow listeners for each app
	client0 := l.workflow.BackendClient(t, ctx) // app0 (orchestrator)
	l.workflow.BackendClientN(t, ctx, 1)        // app1 (activity)

	id, err := client0.ScheduleNewOrchestration(ctx, "MixedWorkflow", api.WithInput("Hello from app0"))
	require.NoError(t, err)
	metadata, err := client0.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)

	assert.True(t, api.OrchestrationMetadataIsComplete(metadata))
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, metadata.RuntimeStatus)
	expectedResult := `"Local processed: Local processed: Remote processed: Local processed: Hello from app0"`
	assert.Equal(t, expectedResult, metadata.GetOutput().GetValue())
}
