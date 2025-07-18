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

package activity

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(appdown))
}

// appdown tests error handling when a target app goes down during execution
type appdown struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	place  *placement.Placement
	sched  *scheduler.Scheduler

	registry1 *task.TaskRegistry
	registry2 *task.TaskRegistry

	activityStarted chan struct{}
	activityReady   chan struct{}
}

func (a *appdown) Setup(t *testing.T) []framework.Option {
	a.activityStarted = make(chan struct{})
	a.activityReady = make(chan struct{})

	a.place = placement.New(t)
	a.sched = scheduler.New(t,
		scheduler.WithLogLevel("debug"))

	app1 := app.New(t)
	app2 := app.New(t)

	a.registry1 = task.NewTaskRegistry()
	a.registry2 = task.NewTaskRegistry()

	// App2: Activity that will be called before the app goes down
	a.registry2.AddActivityN("ProcessData", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app2: %w", err)
		}

		close(a.activityStarted)

		// Block until allowed to proceed (which will never happen in this test)
		// bc triggering this app to go down mid-activity execution and ensure the wf hangs
		<-a.activityReady

		return "Processed by app2: " + input, nil
	})

	a.daprd1 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(a.place.Address()),
		daprd.WithScheduler(a.sched),
		daprd.WithAppPort(app1.Port()),
		daprd.WithLogLevel("debug"),
	)
	a.daprd2 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(a.place.Address()),
		daprd.WithScheduler(a.sched),
		daprd.WithAppPort(app2.Port()),
		daprd.WithLogLevel("debug"),
	)

	// App1: Orchestrator, calls app2
	a.registry1.AddOrchestratorN("AppDownWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in orchestrator: %w", err)
		}

		var result string
		err := ctx.CallActivity("ProcessData",
			task.WithActivityInput(input),
			task.WithActivityAppID(a.daprd2.AppID())).
			Await(&result)
		if err != nil {
			return fmt.Sprintf("Error occurred: %v", err), nil
		}

		return result, nil
	})

	return []framework.Option{
		framework.WithProcesses(a.place, a.sched, app1, app2, a.daprd1),
	}
}

func (a *appdown) Run(t *testing.T, ctx context.Context) {
	a.sched.WaitUntilRunning(t, ctx)
	a.place.WaitUntilRunning(t, ctx)
	a.daprd1.WaitUntilRunning(t, ctx)

	daprd2Ctx, daprd2Cancel := context.WithCancel(t.Context())
	t.Cleanup(daprd2Cancel)
	a.daprd2.Run(t, daprd2Ctx)
	a.daprd2.WaitUntilRunning(t, daprd2Ctx)

	client1 := client.NewTaskHubGrpcClient(a.daprd1.GRPCConn(t, ctx), backend.DefaultLogger())
	client2 := client.NewTaskHubGrpcClient(a.daprd2.GRPCConn(t, ctx), backend.DefaultLogger())

	// Start listeners for each app
	err := client1.StartWorkItemListener(t.Context(), a.registry1)
	require.NoError(t, err)

	cctx, ccancel := context.WithCancel(t.Context())
	t.Cleanup(ccancel)
	err = client2.StartWorkItemListener(cctx, a.registry2)
	require.NoError(t, err)

	id, err := client1.ScheduleNewOrchestration(t.Context(), "AppDownWorkflow", api.WithInput("Hello from app1"))
	require.NoError(t, err)

	select {
	case <-a.activityStarted:
	case <-time.After(15 * time.Second):
		require.Fail(t, "Timed out waiting for activity to start in app2")
	}

	// Stop app2 to simulate app going down mid-execution
	ccancel()
	daprd2Cancel()

	waitCtx, waitCancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer waitCancel()
	_, err = client1.WaitForOrchestrationCompletion(waitCtx, id, api.WithFetchPayloads(true))
	require.Error(t, err)
	assert.EqualError(t, err, "context deadline exceeded")
}
