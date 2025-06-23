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
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(multihop))
}

// multihop demonstrates multi-hop cross-app workflow: app1 → app2 → app3
type multihop struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	daprd3 *daprd.Daprd
	place  *placement.Placement
	sched  *scheduler.Scheduler

	registry1 *task.TaskRegistry
	registry2 *task.TaskRegistry
	registry3 *task.TaskRegistry
}

func (m *multihop) Setup(t *testing.T) []framework.Option {
	m.place = placement.New(t)
	m.sched = scheduler.New(t,
		scheduler.WithLogLevel("debug"))
	db := sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithMetadata("busyTimeout", "10s"),
		sqlite.WithMetadata("disableWAL", "true"),
	)

	app1 := app.New(t)
	app2 := app.New(t)
	app3 := app.New(t)

	// Create registries for each app
	m.registry1 = task.NewTaskRegistry()
	m.registry2 = task.NewTaskRegistry()
	m.registry3 = task.NewTaskRegistry()

	// App2: 1st hop - processes data
	m.registry2.AddActivityN("ProcessData", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app2: %w", err)
		}
		return fmt.Sprintf("Processed by app2: %s", input), nil
	})

	// App3: 2nd hop - transforms data
	m.registry3.AddActivityN("TransformData", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app3: %w", err)
		}
		return fmt.Sprintf("Transformed by app3: %s", input), nil
	})

	// App1: Orchestrator - coordinates the multi-hop workflow
	m.registry1.AddOrchestratorN("MultiHopWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in orchestrator: %w", err)
		}

		// Step 1: Call app2 to process data
		var result1 string
		err := ctx.CallActivity("ProcessData",
			task.WithActivityInput(input),
			task.WithAppID("app2")).
			Await(&result1)
		if err != nil {
			return nil, fmt.Errorf("failed to execute activity in app2: %w", err)
		}

		// Step 2: Call app3 to transform the processed data
		var result2 string
		err = ctx.CallActivity("TransformData",
			task.WithActivityInput(result1),
			task.WithAppID("app3")).
			Await(&result2)
		if err != nil {
			return nil, fmt.Errorf("failed to execute activity in app3: %w", err)
		}

		return result2, nil
	})

	m.daprd1 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(m.place.Address()),
		daprd.WithScheduler(m.sched),
		daprd.WithAppID("app1"),
		daprd.WithAppPort(app1.Port()),
		daprd.WithLogLevel("debug"),
	)
	m.daprd2 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(m.place.Address()),
		daprd.WithScheduler(m.sched),
		daprd.WithAppID("app2"),
		daprd.WithAppPort(app2.Port()),
		daprd.WithLogLevel("debug"),
	)
	m.daprd3 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(m.place.Address()),
		daprd.WithScheduler(m.sched),
		daprd.WithAppID("app3"),
		daprd.WithAppPort(app3.Port()),
		daprd.WithLogLevel("debug"),
	)

	return []framework.Option{
		framework.WithProcesses(m.place, m.sched, db, app1, app2, app3, m.daprd1, m.daprd2, m.daprd3),
	}
}

func (m *multihop) Run(t *testing.T, ctx context.Context) {
	m.sched.WaitUntilRunning(t, ctx)
	m.place.WaitUntilRunning(t, ctx)
	m.daprd1.WaitUntilRunning(t, ctx)
	m.daprd2.WaitUntilRunning(t, ctx)
	m.daprd3.WaitUntilRunning(t, ctx)

	// Start workflow listeners for each app
	client1 := client.NewTaskHubGrpcClient(m.daprd1.GRPCConn(t, ctx), backend.DefaultLogger())
	client2 := client.NewTaskHubGrpcClient(m.daprd2.GRPCConn(t, ctx), backend.DefaultLogger())
	client3 := client.NewTaskHubGrpcClient(m.daprd3.GRPCConn(t, ctx), backend.DefaultLogger())

	// Start listeners for each app
	err := client1.StartWorkItemListener(ctx, m.registry1)
	assert.NoError(t, err)
	err = client2.StartWorkItemListener(ctx, m.registry2)
	assert.NoError(t, err)
	err = client3.StartWorkItemListener(ctx, m.registry3)
	assert.NoError(t, err)

	// Start the multi-hop workflow
	id, err := client1.ScheduleNewOrchestration(ctx, "MultiHopWorkflow", api.WithInput("Hello from app1"))
	assert.NoError(t, err)

	metadata, err := client1.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
	assert.NoError(t, err)

	assert.True(t, api.OrchestrationMetadataIsComplete(metadata))
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, metadata.RuntimeStatus)

	// Verify the final result shows the complete chain
	expectedResult := `"Transformed by app3: Processed by app2: Hello from app1"`
	assert.Equal(t, expectedResult, metadata.GetOutput().GetValue())
}
