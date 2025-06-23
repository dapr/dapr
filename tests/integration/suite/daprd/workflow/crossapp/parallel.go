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
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
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
	suite.Register(new(parallel))
}

// parallel demonstrates parallel cross-app activity calls: app1 → app2, app1 → app3
type parallel struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	daprd3 *daprd.Daprd
	place  *placement.Placement
	sched  *scheduler.Scheduler

	registry1 *task.TaskRegistry
	registry2 *task.TaskRegistry
	registry3 *task.TaskRegistry

	// Shared state for parallel execution verification
	activityStarted atomic.Int32
	releaseCh       chan struct{}
}

func (p *parallel) Setup(t *testing.T) []framework.Option {
	p.place = placement.New(t,
		placement.WithLogLevel("debug"))
	p.sched = scheduler.New(t,
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
	p.registry1 = task.NewTaskRegistry()
	p.registry2 = task.NewTaskRegistry()
	p.registry3 = task.NewTaskRegistry()

	appID1 := uuid.New().String()
	appID2 := uuid.New().String()
	appID3 := uuid.New().String()

	// App2: First parallel activity
	p.registry2.AddActivityN("ProcessData", func(ctx task.ActivityContext) (any, error) {
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

		return fmt.Sprintf("Processed by app2: %s", input), nil
	})

	// App2: Additional parallel activity
	p.registry2.AddActivityN("ProcessData2", func(ctx task.ActivityContext) (any, error) {
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

		return fmt.Sprintf("Processed2 by app2: %s", input), nil
	})

	// App3: Second parallel activity
	p.registry3.AddActivityN("TransformData", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app3: %w", err)
		}

		p.activityStarted.Add(1)
		select {
		case <-p.releaseCh:
		case <-time.After(10 * time.Second):
			return nil, fmt.Errorf("timeout waiting for release")
		}

		return fmt.Sprintf("Transformed by app3: %s", input), nil
	})

	// App3: Additional parallel activity
	p.registry3.AddActivityN("TransformData2", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app3: %w", err)
		}

		p.activityStarted.Add(1)
		select {
		case <-p.releaseCh:
		case <-time.After(10 * time.Second):
			return nil, fmt.Errorf("timeout waiting for release")
		}

		return fmt.Sprintf("Transformed2 by app3: %s", input), nil
	})

	// App1: Orchestrator - calls activities in parallel
	p.registry1.AddOrchestratorN("ParallelWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in orchestrator: %w", err)
		}

		// Start all activities in parallel
		task1 := ctx.CallActivity("ProcessData",
			task.WithActivityInput(input),
			task.WithAppID(appID2))

		task2 := ctx.CallActivity("ProcessData2",
			task.WithActivityInput(input),
			task.WithAppID(appID2))

		task3 := ctx.CallActivity("TransformData",
			task.WithActivityInput(input),
			task.WithAppID(appID3))

		task4 := ctx.CallActivity("TransformData2",
			task.WithActivityInput(input),
			task.WithAppID(appID3))

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

	p.daprd1 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(p.place.Address()),
		daprd.WithScheduler(p.sched),
		daprd.WithAppID(appID1),
		daprd.WithAppPort(app1.Port()),
		daprd.WithLogLevel("debug"),
	)
	p.daprd2 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(p.place.Address()),
		daprd.WithScheduler(p.sched),
		daprd.WithAppID(appID2),
		daprd.WithAppPort(app2.Port()),
		daprd.WithLogLevel("debug"),
	)
	p.daprd3 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(p.place.Address()),
		daprd.WithScheduler(p.sched),
		daprd.WithAppID(appID3),
		daprd.WithAppPort(app3.Port()),
		daprd.WithLogLevel("debug"),
	)

	return []framework.Option{
		framework.WithProcesses(p.place, p.sched, db, app1, app2, app3, p.daprd1, p.daprd2, p.daprd3),
	}
}

func (p *parallel) Run(t *testing.T, ctx context.Context) {
	p.sched.WaitUntilRunning(t, ctx)
	p.place.WaitUntilRunning(t, ctx)
	p.daprd1.WaitUntilRunning(t, ctx)
	p.daprd2.WaitUntilRunning(t, ctx)
	p.daprd3.WaitUntilRunning(t, ctx)

	// Initialize shared state for parallel execution verification
	p.releaseCh = make(chan struct{})
	p.activityStarted.Store(0)

	// Start workflow listeners for each app
	client1 := client.NewTaskHubGrpcClient(p.daprd1.GRPCConn(t, ctx), backend.DefaultLogger())
	client2 := client.NewTaskHubGrpcClient(p.daprd2.GRPCConn(t, ctx), backend.DefaultLogger())
	client3 := client.NewTaskHubGrpcClient(p.daprd3.GRPCConn(t, ctx), backend.DefaultLogger())

	// Start listeners for each app
	err := client1.StartWorkItemListener(ctx, p.registry1)
	assert.NoError(t, err)
	err = client2.StartWorkItemListener(ctx, p.registry2)
	assert.NoError(t, err)
	err = client3.StartWorkItemListener(ctx, p.registry3)
	assert.NoError(t, err)

	// Start the parallel workflow
	id, err := client1.ScheduleNewOrchestration(ctx, "ParallelWorkflow", api.WithInput("Hello from app1"))
	assert.NoError(t, err)

	// Wait for all activities to start
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int32(4), p.activityStarted.Load(), "Expected 4 activities to start")
	}, 10*time.Second, 10*time.Millisecond, "Expected 4 activities to start")

	// Release all activities to complete
	close(p.releaseCh)

	metadata, err := client1.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
	assert.NoError(t, err)
	assert.True(t, api.OrchestrationMetadataIsComplete(metadata))
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, metadata.RuntimeStatus)
	expectedResult := `"Combined: Processed by app2: Hello from app1 | Processed2 by app2: Hello from app1 | Transformed by app3: Hello from app1 | Transformed2 by app3: Hello from app1"`
	assert.Equal(t, expectedResult, metadata.GetOutput().GetValue())
}
