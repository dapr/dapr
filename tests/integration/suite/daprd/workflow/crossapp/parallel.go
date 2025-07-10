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
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(parallel))
}

// parallel demonstrates parallel cross-app activity calls: app0 → app1, app0 → app2
type parallel struct {
	workflow *workflow.Workflow

	// Shared state for parallel execution verification
	activityStarted atomic.Int32
	releaseCh       chan struct{}
}

func (p *parallel) Setup(t *testing.T) []framework.Option {
	p.releaseCh = make(chan struct{})
	p.workflow = workflow.New(t,
		workflow.WithDaprds(3),
	)

	return []framework.Option{
		framework.WithProcesses(p.workflow),
	}
}

func (p *parallel) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	// App1: First parallel activity
	p.workflow.RegistryN(1).AddActivityN("ProcessData", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app1: %w", err)
		}

		p.activityStarted.Add(1)
		select {
		case <-p.releaseCh:
		case <-time.After(10 * time.Second):
			return nil, errors.New("timeout waiting for release")
		}

		return fmt.Sprintf("Processed by app1: %s", input), nil
	})

	// App1: Additional parallel activity
	p.workflow.RegistryN(1).AddActivityN("ProcessData2", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app1: %w", err)
		}

		p.activityStarted.Add(1)
		select {
		case <-p.releaseCh:
		case <-time.After(10 * time.Second):
			return nil, fmt.Errorf("timeout waiting for release")
		}

		return fmt.Sprintf("Processed2 by app1: %s", input), nil
	})

	// App2: Second parallel activity
	p.workflow.RegistryN(2).AddActivityN("TransformData", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app2: %w", err)
		}

		p.activityStarted.Add(1)
		select {
		case <-p.releaseCh:
		case <-time.After(10 * time.Second):
			return nil, fmt.Errorf("timeout waiting for release")
		}

		return fmt.Sprintf("Transformed by app2: %s", input), nil
	})

	// App2: Additional parallel activity
	p.workflow.RegistryN(2).AddActivityN("TransformData2", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app2: %w", err)
		}

		p.activityStarted.Add(1)
		select {
		case <-p.releaseCh:
		case <-time.After(10 * time.Second):
			return nil, fmt.Errorf("timeout waiting for release")
		}

		return fmt.Sprintf("Transformed2 by app2: %s", input), nil
	})

	// App0: Orchestrator - calls activities in parallel
	p.workflow.Registry().AddOrchestratorN("ParallelWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in orchestrator: %w", err)
		}

		// Start all activities in parallel
		task1 := ctx.CallActivity("ProcessData",
			task.WithActivityInput(input),
			task.WithAppID(p.workflow.DaprN(1).AppID()))

		task2 := ctx.CallActivity("ProcessData2",
			task.WithActivityInput(input),
			task.WithAppID(p.workflow.DaprN(1).AppID()))

		task3 := ctx.CallActivity("TransformData",
			task.WithActivityInput(input),
			task.WithAppID(p.workflow.DaprN(2).AppID()))

		task4 := ctx.CallActivity("TransformData2",
			task.WithActivityInput(input),
			task.WithAppID(p.workflow.DaprN(2).AppID()))

		// Wait for all to complete
		var result1, result2, result3, result4 string
		err := task1.Await(&result1)
		if err != nil {
			return nil, fmt.Errorf("failed to execute ProcessData: %w", err)
		}

		err = task2.Await(&result2)
		if err != nil {
			return nil, fmt.Errorf("failed to execute ProcessData2: %w", err)
		}

		err = task3.Await(&result3)
		if err != nil {
			return nil, fmt.Errorf("failed to execute TransformData: %w", err)
		}

		err = task4.Await(&result4)
		if err != nil {
			return nil, fmt.Errorf("failed to execute TransformData2: %w", err)
		}
		return fmt.Sprintf("Combined: %s | %s | %s | %s", result1, result2, result3, result4), nil
	})

	// Start workflow listeners for each app
	client0 := p.workflow.BackendClient(t, ctx) // app0 (orchestrator)
	p.workflow.BackendClientN(t, ctx, 1)        // app1 (activities)
	p.workflow.BackendClientN(t, ctx, 2)        // app2 (activities)

	id, err := client0.ScheduleNewOrchestration(ctx, "ParallelWorkflow", api.WithInput("test input"))
	require.NoError(t, err)
	// Wait for all activities to start (they should start in parallel and complete)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int32(4), p.activityStarted.Load(), "Expected 4 activities to start")
	}, 10*time.Second, 10*time.Millisecond, "Expected 4 activities to start")
	close(p.releaseCh)

	metadata, err := client0.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, metadata.RuntimeStatus)
	assert.True(t, api.OrchestrationMetadataIsComplete(metadata))
	expectedOutput := `"Combined: Processed by app1: test input | Processed2 by app1: test input | Transformed by app2: test input | Transformed2 by app2: test input"`
	assert.Equal(t, expectedOutput, metadata.GetOutput().GetValue())
}
