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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(basiclogline))
}

// basiclogline demonstrates calling activities across different Dapr applications.
type basiclogline struct {
	workflow *workflow.Workflow
	logline0 *logline.LogLine
	logline1 *logline.LogLine
}

func (b *basiclogline) Setup(t *testing.T) []framework.Option {
	// Create loglines first
	b.logline0 = logline.New(t,
		logline.WithStdoutLineContains(
			"invoking execute method on activity actor",
			"workflow completed with status 'ORCHESTRATION_STATUS_COMPLETED' workflowName 'CrossAppWorkflow'",
		),
	)
	b.logline1 = logline.New(t,
		logline.WithStdoutLineContains(
			"Activity actor",
			"::0::1': invoking method 'Execute'",
			"activity completed for workflow with instanceId",
		),
	)

	b.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithExecOptions(
			exec.WithStdout(b.logline0.Stdout()),
		)),
		workflow.WithDaprdOptions(1, daprd.WithExecOptions(
			exec.WithStdout(b.logline1.Stdout()),
		)),
	)

	return []framework.Option{
		framework.WithProcesses(b.logline0, b.logline1, b.workflow),
	}
}

func (b *basiclogline) Run(t *testing.T, ctx context.Context) {
	b.workflow.WaitUntilRunning(t, ctx)
	
	// Add orchestrator to app0's registry
	b.workflow.Registry(0).AddOrchestratorN("CrossAppWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app0: %w", err)
		}

		var output string
		err := ctx.CallActivity("ProcessData",
			task.WithActivityInput(input),
			task.WithAppID(b.workflow.DaprN(1).AppID())).
			Await(&output)
		if err != nil {
			return nil, fmt.Errorf("failed to execute activity in app1: %w", err)
		}
		return output, nil
	})

	// Add activity to app1's registry
	b.workflow.Registry(1).AddActivityN("ProcessData", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app1 activity: %w", err)
		}
		return fmt.Sprintf("Processed by app1: %s", input), nil
	})

	// Start workflow listeners for each app with their respective registries
	client0 := b.workflow.BackendClient(t, ctx, 0) // app0 (orchestrator)
	b.workflow.BackendClient(t, ctx, 1)            // app1 (activity)

	id, err := client0.ScheduleNewOrchestration(ctx, "CrossAppWorkflow", api.WithInput("Hello from app0"))
	require.NoError(t, err)

	// Wait for completion with timeout
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	metadata, err := client0.WaitForOrchestrationCompletion(waitCtx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.OrchestrationMetadataIsComplete(metadata))
	assert.Equal(t, `"Processed by app1: Hello from app0"`, metadata.GetOutput().GetValue())
	b.logline0.EventuallyFoundAll(t)
	b.logline1.EventuallyFoundAll(t)
}
