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
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(restartmanyapps))
}

type restartmanyapps struct {
	workflow *workflow.Workflow
}

func (r *restartmanyapps) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t,
		workflow.WithDaprds(3),
	)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *restartmanyapps) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	// App1: First activity
	r.workflow.Registry(1).AddActivityN("process", func(c task.ActivityContext) (any, error) {
		var input string
		if err := c.GetInput(&input); err != nil {
			return nil, err
		}
		return fmt.Sprintf("processed by app1: %s", input), nil
	})

	// App2: Second activity
	r.workflow.Registry(2).AddActivityN("transform", func(c task.ActivityContext) (any, error) {
		var input string
		if err := c.GetInput(&input); err != nil {
			return nil, err
		}
		return fmt.Sprintf("transformed by app2: %s", input), nil
	})

	// App0: Orchestrator that calls both app1 & app2
	r.workflow.Registry(0).AddOrchestratorN("multiapp", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}

		// Call app1
		var result1 string
		err := ctx.CallActivity("process",
			task.WithActivityInput(input),
			task.WithAppID(r.workflow.DaprN(1).AppID())).Await(&result1)
		if err != nil {
			return nil, err
		}

		// Call app2
		var result2 string
		err = ctx.CallActivity("transform",
			task.WithActivityInput(result1),
			task.WithAppID(r.workflow.DaprN(2).AppID())).Await(&result2)
		if err != nil {
			return nil, err
		}

		return result2, nil
	})

	timeTaken := make([]time.Duration, 0, 3)
	for range 3 {
		// Start workflow listeners for each app with their respective registries
		client0 := r.workflow.BackendClient(t, ctx, 0) // app0 (orchestrator)
		r.workflow.BackendClient(t, ctx, 1)            // app1 (activity)
		r.workflow.BackendClient(t, ctx, 2)            // app2 (activity)

		now := time.Now()
		id, err := client0.ScheduleNewOrchestration(ctx, "multiapp",
			api.WithInstanceID("multiapp-restart"),
			api.WithInput("hello"))
		require.NoError(t, err)
		metadata, err := client0.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		timeTaken = append(timeTaken, time.Since(now))

		expected := `"transformed by app2: processed by app1: hello"`
		assert.Equal(t, expected, metadata.GetOutput().GetValue())
	}

	// Ensure all workflows take similar amounts of time.
	for _, d1 := range timeTaken {
		for _, d2 := range timeTaken {
			assert.InDelta(t, d1.Seconds(), d2.Seconds(), float64(time.Second))
		}
	}
}
