/*
Copyright 2026 The Dapr Authors
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

package pending

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
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(multiremote))
}

// multiremote tests a workflow that fans out to two remote apps where one is
// online and the other is offline. The workflow should transition to RUNNING
// and complete once the offline app comes online.
type multiremote struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	daprd3 *daprd.Daprd
	place  *placement.Placement
	sched  *scheduler.Scheduler

	registry1 *task.TaskRegistry
	registry2 *task.TaskRegistry
	registry3 *task.TaskRegistry
}

func (m *multiremote) Setup(t *testing.T) []framework.Option {
	m.place = placement.New(t)
	m.sched = scheduler.New(t)

	app1 := app.New(t)
	app2 := app.New(t)
	app3 := app.New(t)

	m.registry1 = task.NewTaskRegistry()
	m.registry2 = task.NewTaskRegistry()
	m.registry3 = task.NewTaskRegistry()

	m.daprd1 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(m.place.Address()),
		daprd.WithScheduler(m.sched),
		daprd.WithAppPort(app1.Port()),
		daprd.WithLogLevel("debug"),
	)
	m.daprd2 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(m.place.Address()),
		daprd.WithScheduler(m.sched),
		daprd.WithAppPort(app2.Port()),
		daprd.WithLogLevel("debug"),
	)
	m.daprd3 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(m.place.Address()),
		daprd.WithScheduler(m.sched),
		daprd.WithAppPort(app3.Port()),
		daprd.WithLogLevel("debug"),
	)

	m.registry1.AddOrchestratorN("MultiRemoteWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}

		onlineTask := ctx.CallActivity("OnlineActivity",
			task.WithActivityInput(input),
			task.WithActivityAppID(m.daprd2.AppID()))
		offlineTask := ctx.CallActivity("OfflineActivity",
			task.WithActivityInput(input),
			task.WithActivityAppID(m.daprd3.AppID()))

		var onlineResult string
		if err := onlineTask.Await(&onlineResult); err != nil {
			return nil, fmt.Errorf("online activity failed: %w", err)
		}
		var offlineResult string
		if err := offlineTask.Await(&offlineResult); err != nil {
			return nil, fmt.Errorf("offline activity failed: %w", err)
		}
		return onlineResult + "," + offlineResult, nil
	})

	m.registry2.AddActivityN("OnlineActivity", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		return "online:" + input, nil
	})

	m.registry3.AddActivityN("OfflineActivity", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		return "offline:" + input, nil
	})

	return []framework.Option{
		framework.WithProcesses(m.place, m.sched, app1, app2, app3, m.daprd1, m.daprd2),
	}
}

func (m *multiremote) Run(t *testing.T, ctx context.Context) {
	m.sched.WaitUntilRunning(t, ctx)
	m.place.WaitUntilRunning(t, ctx)
	m.daprd1.WaitUntilRunning(t, ctx)
	m.daprd2.WaitUntilRunning(t, ctx)

	client1 := client.NewTaskHubGrpcClient(m.daprd1.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, client1.StartWorkItemListener(ctx, m.registry1))

	client2 := client.NewTaskHubGrpcClient(m.daprd2.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, client2.StartWorkItemListener(ctx, m.registry2))

	id, err := client1.ScheduleNewOrchestration(ctx, "MultiRemoteWorkflow", api.WithInput("hello"))
	require.NoError(t, err)

	metadata, err := client1.WaitForOrchestrationStart(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_RUNNING, metadata.RuntimeStatus)

	m.daprd3.Run(t, ctx)
	t.Cleanup(func() { m.daprd3.Cleanup(t) })
	m.daprd3.WaitUntilRunning(t, ctx)

	client3 := client.NewTaskHubGrpcClient(m.daprd3.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, client3.StartWorkItemListener(ctx, m.registry3))

	metadata, err = client1.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.OrchestrationMetadataIsComplete(metadata))
	assert.Equal(t, `"online:hello,offline:hello"`, metadata.GetOutput().GetValue())
}
