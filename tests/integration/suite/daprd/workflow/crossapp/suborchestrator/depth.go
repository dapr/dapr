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

package suborchestrator

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
	suite.Register(new(depth))
}

// depth demonstrates deep nested cross-app suborchestrator and activity calls.
//
// The complete run looks like this:
// Workflow0 (runs in app0)
//   LocalActivity0 (runs in app0)
//   RemoteActivity3 (runs in app3)
//   Workflow1 (runs in app1)
//     LocalActivity1 (runs in app1)
//     RemoteActivity4 (runs in app4)
//     Workflow2 (runs in app2)
//       LocalActivity2 (runs in app2)
//       RemoteActivity5 (runs in app5)

type depth struct {
	workflow *workflow.Workflow
}

func (d *depth) Setup(t *testing.T) []framework.Option {
	d.workflow = workflow.New(t,
		workflow.WithDaprds(6))

	return []framework.Option{
		framework.WithProcesses(d.workflow),
	}
}

func (d *depth) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)

	for i := range 3 {
		// apps 0,1,2 have activities as well as orchestrators
		localActivityName := fmt.Sprintf("LocalActivity%d", i)
		d.workflow.RegistryN(i).AddActivityN(localActivityName, func(ctx task.ActivityContext) (any, error) {
			return localActivityName, nil
		})

		remoteActivityName := fmt.Sprintf("RemoteActivity%d", 3+i)
		// apps 3,4,5 only have activities
		d.workflow.RegistryN(3+i).AddActivityN(remoteActivityName, func(ctx task.ActivityContext) (any, error) {
			return remoteActivityName, nil
		})
	}

	d.workflow.Registry().AddOrchestratorN("Workflow0", func(ctx *task.OrchestrationContext) (any, error) {
		var result0 string
		err := ctx.CallActivity("LocalActivity0").Await(&result0)
		if err != nil {
			return nil, fmt.Errorf("failed to execute local activity in app0: %w", err)
		}

		var result3 string
		err = ctx.CallActivity("RemoteActivity3", task.WithActivityAppID(d.workflow.DaprN(3).AppID())).Await(&result3)
		if err != nil {
			return nil, fmt.Errorf("failed to execute remote activity in app3: %w", err)
		}

		var result1 string
		err = ctx.CallSubOrchestrator("Workflow1", task.WithSubOrchestratorAppID(d.workflow.DaprN(1).AppID())).Await(&result1)
		if err != nil {
			return nil, fmt.Errorf("failed to execute suborchestrator in app1: %w", err)
		}

		return fmt.Sprintf("Workflow0: %s %s %s", result0, result3, result1), nil
	})

	d.workflow.RegistryN(1).AddOrchestratorN("Workflow1", func(ctx *task.OrchestrationContext) (any, error) {
		var result1 string
		err := ctx.CallActivity("LocalActivity1").Await(&result1)
		if err != nil {
			return nil, fmt.Errorf("failed to execute local activity in app1: %w", err)
		}

		var result4 string
		err = ctx.CallActivity("RemoteActivity4", task.WithActivityAppID(d.workflow.DaprN(4).AppID())).Await(&result4)
		if err != nil {
			return nil, fmt.Errorf("failed to execute remote activity in app4: %w", err)
		}

		var result2 string
		err = ctx.CallSubOrchestrator("Workflow2", task.WithSubOrchestratorAppID(d.workflow.DaprN(2).AppID())).Await(&result2)
		if err != nil {
			return nil, fmt.Errorf("failed to execute suborchestrator in app2: %w", err)
		}

		return fmt.Sprintf("Workflow1: %s %s %s", result1, result4, result2), nil
	})

	d.workflow.RegistryN(2).AddOrchestratorN("Workflow2", func(ctx *task.OrchestrationContext) (any, error) {
		var result2 string
		err := ctx.CallActivity("LocalActivity2").Await(&result2)
		if err != nil {
			return nil, fmt.Errorf("failed to execute local activity in app2: %w", err)
		}

		var result5 string
		err = ctx.CallActivity("RemoteActivity5", task.WithActivityAppID(d.workflow.DaprN(5).AppID())).Await(&result5)
		if err != nil {
			return nil, fmt.Errorf("failed to execute remote activity in app5: %w", err)
		}

		return fmt.Sprintf("Workflow2: %s %s", result2, result5), nil
	})

	// Start the deep nested workflow
	client0 := d.workflow.BackendClient(t, ctx) // app1
	d.workflow.BackendClientN(t, ctx, 1)        // app2
	d.workflow.BackendClientN(t, ctx, 2)        // app3
	d.workflow.BackendClientN(t, ctx, 3)        // app4
	d.workflow.BackendClientN(t, ctx, 4)        // app5
	d.workflow.BackendClientN(t, ctx, 5)        // app6

	id, err := client0.ScheduleNewOrchestration(ctx, "Workflow0", api.WithInput("Hello from app1"))
	require.NoError(t, err)

	metadata, err := client0.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)

	assert.True(t, api.OrchestrationMetadataIsComplete(metadata))
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, metadata.RuntimeStatus)

	// Verify the final result shows the complete nested chain
	expectedResult := `"Workflow0: LocalActivity0 RemoteActivity3 Workflow1: LocalActivity1 RemoteActivity4 Workflow2: LocalActivity2 RemoteActivity5"`
	assert.Equal(t, expectedResult, metadata.GetOutput().GetValue())
}
