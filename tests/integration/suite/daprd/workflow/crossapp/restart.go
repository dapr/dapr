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
	suite.Register(new(restart))
}

// restart tests when a target app goes down during activity
// execution and eventually comes back up, testing transient issues
type restart struct {
	daprd1   *daprd.Daprd
	daprd2   *daprd.Daprd
	place    *placement.Placement
	sched    *scheduler.Scheduler
	appID2   string
	app2Port int

	registry1 *task.TaskRegistry
	registry2 *task.TaskRegistry

	activityStarted chan struct{}
}

func (r *restart) Setup(t *testing.T) []framework.Option {
	r.activityStarted = make(chan struct{})

	r.place = placement.New(t)
	r.sched = scheduler.New(t,
		scheduler.WithLogLevel("debug"))
	db := sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithMetadata("busyTimeout", "10s"),
		sqlite.WithMetadata("disableWAL", "true"),
	)

	app1 := app.New(t)
	app2 := app.New(t)

	r.registry1 = task.NewTaskRegistry()
	r.registry2 = task.NewTaskRegistry()

	r.appID2 = uuid.New().String()
	r.app2Port = app2.Port()

	// App2
	r.registry2.AddActivityN("ProcessData", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app2: %w", err)
		}

		select {
		case r.activityStarted <- struct{}{}:
		default:
		}
		return "Processed by app2: " + input, nil
	})

	r.daprd1 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithScheduler(r.sched),
		daprd.WithAppPort(app1.Port()),
		daprd.WithLogLevel("debug"),
	)
	r.daprd2 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithScheduler(r.sched),
		daprd.WithAppPort(app2.Port()),
		daprd.WithLogLevel("debug"),
		daprd.WithAppID(r.appID2),
	)

	// App1: Orchestrator, calls app2
	r.registry1.AddOrchestratorN("restartWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in orchestrator: %w", err)
		}

		var result string
		err := ctx.CallActivity("ProcessData",
			task.WithActivityInput(input),
			task.WithAppID(r.daprd2.AppID())).
			Await(&result)
		if err != nil {
			return fmt.Sprintf("Error occurred: %v", err), nil
		}
		return result, nil
	})

	return []framework.Option{
		framework.WithProcesses(r.place, r.sched, db, app1, app2, r.daprd1, r.daprd2),
	}
}

func (r *restart) Run(t *testing.T, ctx context.Context) {
	r.sched.WaitUntilRunning(t, ctx)
	r.place.WaitUntilRunning(t, ctx)
	r.daprd1.WaitUntilRunning(t, ctx)
	r.daprd2.WaitUntilRunning(t, ctx)

	wctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Start workflow listeners for each app
	client1 := client.NewTaskHubGrpcClient(r.daprd1.GRPCConn(t, ctx), backend.DefaultLogger())
	client2 := client.NewTaskHubGrpcClient(r.daprd2.GRPCConn(t, wctx), backend.DefaultLogger())
	require.NoError(t, client1.StartWorkItemListener(ctx, r.registry1))
	require.NoError(t, client2.StartWorkItemListener(wctx, r.registry2))

	id, err := client1.ScheduleNewOrchestration(context.Background(), "restartWorkflow", api.WithInput("Hello from app1"))
	require.NoError(t, err)
	select {
	case <-r.activityStarted:
	case <-time.After(20 * time.Second):
		t.Fatal("Timeout waiting for activity to start")
	}

	// Stop app2 to simulate app going down mid-execution
	cancel()
	r.daprd2.Cleanup(t)

	// Expect completion to hang, so timeout
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer waitCancel()
	_, err = client1.WaitForOrchestrationCompletion(waitCtx, id, api.WithFetchPayloads(true))
	require.Error(t, err)
	assert.EqualError(t, err, "context deadline exceeded")

	// Create a new daprd2 instance, for restart
	r.daprd2 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithScheduler(r.sched),
		daprd.WithAppPort(r.app2Port),
		daprd.WithAppID(r.appID2),
		daprd.WithLogLevel("debug"),
	)
	r.daprd2.Run(t, ctx)
	r.daprd2.WaitUntilRunning(t, ctx)
	t.Cleanup(func() {
		r.daprd2.Cleanup(t)
	})

	// Restart the listener for app2 & ensure wf completion
	client2Restart := client.NewTaskHubGrpcClient(r.daprd2.GRPCConn(t, context.Background()), backend.DefaultLogger())
	require.NoError(t, client2Restart.StartWorkItemListener(context.Background(), r.registry2))
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		completionCtx, completionCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer completionCancel()

		_, err := client1.WaitForOrchestrationCompletion(completionCtx, id, api.WithFetchPayloads(true))
		if err != nil {
			return
		}
	}, 20*time.Second, 100*time.Millisecond)
}
