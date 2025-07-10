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
	suite.Register(new(crossnamespace))
}

// crossnamespace tests that calling activities across different namespaces fails
type crossnamespace struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	place  *placement.Placement
	sched  *scheduler.Scheduler

	registry1 *task.TaskRegistry
	registry2 *task.TaskRegistry
}

func (c *crossnamespace) Setup(t *testing.T) []framework.Option {
	c.place = placement.New(t)
	c.sched = scheduler.New(t,
		scheduler.WithLogLevel("debug"))
	db := sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithMetadata("busyTimeout", "10s"),
		sqlite.WithMetadata("disableWAL", "true"),
	)

	app1 := app.New(t)
	app2 := app.New(t)

	c.registry1 = task.NewTaskRegistry()
	c.registry2 = task.NewTaskRegistry()

	appID1 := uuid.New().String()
	appID2 := uuid.New().String()

	// App2: Activity in different namespace
	c.registry2.AddActivityN("ProcessData", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app2 activity: %w", err)
		}
		return "Processed by app2: " + input, nil
	})

	// App1: Orchestrator that tries to call app2 in different namespace
	c.registry1.AddOrchestratorN("CrossNamespaceWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app1: %w", err)
		}

		// Try to call activity on app2 in different namespace - this should fail
		var output string
		err := ctx.CallActivity("ProcessData",
			task.WithActivityInput(input),
			task.WithAppID(appID2)).
			Await(&output)
		if err != nil {
			// Expected to fail due to namespace isolation
			return fmt.Sprintf("Cross-namespace call failed as expected: %v", err), nil
		}
		return output, nil
	})

	// App1: In "default" namespace
	c.daprd1 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(c.place.Address()),
		daprd.WithScheduler(c.sched),
		daprd.WithAppID(appID1),
		daprd.WithAppPort(app1.Port()),
		daprd.WithLogLevel("debug"),
		daprd.WithNamespace("default"),
	)

	// App2: In "other" namespace
	c.daprd2 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(c.place.Address()),
		daprd.WithScheduler(c.sched),
		daprd.WithAppID(appID2),
		daprd.WithAppPort(app2.Port()),
		daprd.WithLogLevel("debug"),
		daprd.WithNamespace("other"),
	)

	return []framework.Option{
		framework.WithProcesses(c.place, c.sched, db, app1, app2, c.daprd1, c.daprd2),
	}
}

func (c *crossnamespace) Run(t *testing.T, ctx context.Context) {
	c.sched.WaitUntilRunning(t, ctx)
	c.place.WaitUntilRunning(t, ctx)
	c.daprd1.WaitUntilRunning(t, ctx)
	c.daprd2.WaitUntilRunning(t, ctx)

	client1 := client.NewTaskHubGrpcClient(c.daprd1.GRPCConn(t, ctx), backend.DefaultLogger())
	client2 := client.NewTaskHubGrpcClient(c.daprd2.GRPCConn(t, ctx), backend.DefaultLogger())

	err := client1.StartWorkItemListener(ctx, c.registry1)
	require.NoError(t, err)
	err = client2.StartWorkItemListener(ctx, c.registry2)
	require.NoError(t, err)

	// Expect completion to hang, so timeout
	// dapr will log about 'did not find address for actor'
	waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
	defer waitCancel()

	// Start workflow from app1 (default namespace)
	_, err = client1.ScheduleNewOrchestration(waitCtx, "CrossNamespaceWorkflow", api.WithInput("Hello from app1"))
	assert.Error(t, err)
	assert.EqualError(t, err, "context deadline exceeded")
}
