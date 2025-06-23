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

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	suite.Register(new(localmix))
}

// localmix demonstrates mixing local and cross-app activity calls
type localmix struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	place  *placement.Placement
	sched  *scheduler.Scheduler

	registry1 *task.TaskRegistry
	registry2 *task.TaskRegistry
}

func (l *localmix) Setup(t *testing.T) []framework.Option {
	l.place = placement.New(t,
		placement.WithLogLevel("debug"))
	l.sched = scheduler.New(t,
		scheduler.WithLogLevel("debug"))
	db := sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithMetadata("busyTimeout", "10s"),
		sqlite.WithMetadata("disableWAL", "true"),
	)

	app1 := app.New(t)
	app2 := app.New(t)

	// Create registries for each app
	l.registry1 = task.NewTaskRegistry()
	l.registry2 = task.NewTaskRegistry()

	appID1 := uuid.New().String()
	appID2 := uuid.New().String()

	// App1: Local activity (no AppID specified)
	l.registry1.AddActivityN("LocalProcess1", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in local activity: %w", err)
		}
		return fmt.Sprintf("Local processed: %s", input), nil
	})

	// App2: Cross-app activity
	l.registry2.AddActivityN("RemoteProcess2", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in remote activity: %w", err)
		}
		return fmt.Sprintf("Remote processed: %s", input), nil
	})

	// App1: Local activity 3 (no AppID specified)
	l.registry1.AddActivityN("LocalProcess3", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in local activity: %w", err)
		}
		return fmt.Sprintf("Local processed: %s", input), nil
	})

	// App1: Local activity 4 (no AppID specified)
	l.registry1.AddActivityN("LocalProcess4", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in local activity: %w", err)
		}
		return fmt.Sprintf("Local processed: %s", input), nil
	})

	// App1: Orchestrator - mixes local & cross-app calls
	l.registry1.AddOrchestratorN("MixedWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
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
			task.WithAppID(appID2)).
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
			task.WithAppID(appID1)).
			Await(&step4Result)
		if err != nil {
			return nil, fmt.Errorf("failed to execute step 4 local activity: %w", err)
		}

		return step4Result, nil
	})

	l.daprd1 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(l.place.Address()),
		daprd.WithScheduler(l.sched),
		daprd.WithAppID(appID1),
		daprd.WithAppPort(app1.Port()),
		daprd.WithLogLevel("debug"),
	)
	l.daprd2 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(l.place.Address()),
		daprd.WithScheduler(l.sched),
		daprd.WithAppID(appID2),
		daprd.WithAppPort(app2.Port()),
		daprd.WithLogLevel("debug"),
	)

	return []framework.Option{
		framework.WithProcesses(l.place, l.sched, db, app1, app2, l.daprd1, l.daprd2),
	}
}

func (l *localmix) Run(t *testing.T, ctx context.Context) {
	l.sched.WaitUntilRunning(t, ctx)
	l.place.WaitUntilRunning(t, ctx)
	l.daprd1.WaitUntilRunning(t, ctx)
	l.daprd2.WaitUntilRunning(t, ctx)

	// Start workflow listeners for each app
	client1 := client.NewTaskHubGrpcClient(l.daprd1.GRPCConn(t, ctx), backend.DefaultLogger())
	client2 := client.NewTaskHubGrpcClient(l.daprd2.GRPCConn(t, ctx), backend.DefaultLogger())

	// Start listeners for each app
	err := client1.StartWorkItemListener(ctx, l.registry1)
	assert.NoError(t, err)
	err = client2.StartWorkItemListener(ctx, l.registry2)
	assert.NoError(t, err)

	id, err := client1.ScheduleNewOrchestration(ctx, "MixedWorkflow", api.WithInput("Hello from app1"))
	require.NoError(t, err)
	metadata, err := client1.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)

	assert.True(t, api.OrchestrationMetadataIsComplete(metadata))
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, metadata.RuntimeStatus)
	expectedResult := `"Local processed: Local processed: Remote processed: Local processed: Hello from app1"`
	assert.Equal(t, expectedResult, metadata.GetOutput().GetValue())
}
