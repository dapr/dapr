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

package security

import (
	"context"
	"fmt"
	"testing"

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
	suite.Register(new(security))
}

type security struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	place  *placement.Placement
	sched  *scheduler.Scheduler

	registry1 *task.TaskRegistry
	registry2 *task.TaskRegistry
}

func (c *security) Setup(t *testing.T) []framework.Option {
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

	// App1 workflow that processes data locally
	c.registry1.AddOrchestratorN("App1Workflow", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app1: %w", err)
		}
		return fmt.Sprintf("Processed by app1: %s", input), nil
	})

	// App2 workflow that processes data locally
	c.registry2.AddOrchestratorN("App2Workflow", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app2: %w", err)
		}
		return fmt.Sprintf("Processed by app2: %s", input), nil
	})

	c.daprd1 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(c.place.Address()),
		daprd.WithScheduler(c.sched),
		daprd.WithAppPort(app1.Port()),
		daprd.WithLogLevel("debug"),
	)

	c.daprd2 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(c.place.Address()),
		daprd.WithScheduler(c.sched),
		daprd.WithAppPort(app2.Port()),
		daprd.WithLogLevel("debug"),
	)

	return []framework.Option{
		framework.WithProcesses(c.place, c.sched, db, app1, app2, c.daprd1, c.daprd2),
	}
}

func (c *security) Run(t *testing.T, ctx context.Context) {
	c.sched.WaitUntilRunning(t, ctx)
	c.place.WaitUntilRunning(t, ctx)
	c.daprd1.WaitUntilRunning(t, ctx)
	c.daprd2.WaitUntilRunning(t, ctx)

	// Start workflow listeners for each app
	client1 := client.NewTaskHubGrpcClient(c.daprd1.GRPCConn(t, ctx), backend.DefaultLogger())
	client2 := client.NewTaskHubGrpcClient(c.daprd2.GRPCConn(t, ctx), backend.DefaultLogger())

	err := client1.StartWorkItemListener(ctx, c.registry1)
	assert.NoError(t, err)
	err = client2.StartWorkItemListener(ctx, c.registry2)
	assert.NoError(t, err)

	id1, err := client1.ScheduleNewOrchestration(ctx, "App1Workflow", api.WithInput("Hello from app1"))
	require.NoError(t, err)
	id2, err := client2.ScheduleNewOrchestration(ctx, "App2Workflow", api.WithInput("Hello from app2"))
	require.NoError(t, err)

	_, err = client1.WaitForOrchestrationStart(ctx, id1)
	require.NoError(t, err)
	_, err = client2.WaitForOrchestrationStart(ctx, id2)
	require.NoError(t, err)

	// Test that App2 cannot access App1's orchestration metadata or orchestration
	_, err = client2.FetchOrchestrationMetadata(ctx, id1, api.WithFetchPayloads(true))
	require.Error(t, err, "App2 should not be able to access App1's orchestration metadata")
	err = client2.SuspendOrchestration(ctx, id1, "myreason")
	require.Error(t, err, "App2 should not be able to control App1's orchestration")

	// Test that App1 cannot access App2's orchestration metadata or orchesstration
	_, err = client1.FetchOrchestrationMetadata(ctx, id2, api.WithFetchPayloads(true))
	require.Error(t, err, "App1 should not be able to access App2's orchestration metadata")
	err = client1.SuspendOrchestration(ctx, id2, "myreason")
	require.Error(t, err, "App1 should not be able to control App2's orchestration")

	// Wait for both workflows
	metadata1, err := client1.WaitForOrchestrationCompletion(ctx, id1, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.OrchestrationMetadataIsComplete(metadata1))
	assert.Equal(t, `"Processed by app1: Hello from app1"`, metadata1.GetOutput().GetValue())
	metadata2, err := client2.WaitForOrchestrationCompletion(ctx, id2, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.OrchestrationMetadataIsComplete(metadata2))
	assert.Equal(t, `"Processed by app2: Hello from app2"`, metadata2.GetOutput().GetValue())
}
