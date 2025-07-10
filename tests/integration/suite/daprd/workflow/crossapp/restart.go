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
	suite.Register(new(restart))
}

type restart struct {
	workflow *workflow.Workflow
}

func (r *restart) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t,
		workflow.WithDaprds(2),
	)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *restart) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	// Add orchestrator to app0's registry
	r.workflow.Registry().AddOrchestratorN("foo", func(ctx *task.OrchestrationContext) (any, error) {
		var result string
		err := ctx.CallActivity("bar",
			task.WithAppID(r.workflow.DaprN(1).AppID()),
		).Await(&result)
		if err != nil {
			return nil, err
		}
		return result, nil
	})

	// Add activity to app1's registry
	r.workflow.RegistryN(1).AddActivityN("bar", func(c task.ActivityContext) (any, error) {
		return "processed by app1", nil
	})

	timeTaken := make([]time.Duration, 0, 5)
	for range 5 {
		// Start workflow listeners for each app with their respective registries
		client0 := r.workflow.BackendClient(t, ctx) // app0 (orchestrator)
		r.workflow.BackendClientN(t, ctx, 1)        // app1 (activity)

		now := time.Now()
		id, err := client0.ScheduleNewOrchestration(ctx, "foo", api.WithInstanceID("crossapp-restart"))
		require.NoError(t, err)
		_, err = client0.WaitForOrchestrationCompletion(ctx, id)
		require.NoError(t, err)
		timeTaken = append(timeTaken, time.Since(now))
	}

	// Ensure all workflows take similar amounts of time.
	for _, d1 := range timeTaken {
		for _, d2 := range timeTaken {
			assert.InDelta(t, d1.Seconds(), d2.Seconds(), float64(time.Second))
		}
	}
}
