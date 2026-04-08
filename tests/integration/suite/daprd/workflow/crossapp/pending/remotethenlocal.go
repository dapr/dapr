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
	"sync/atomic"
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
	suite.Register(new(remotethenlocal))
}

type remotethenlocal struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	place  *placement.Placement
	sched  *scheduler.Scheduler

	registry1 *task.TaskRegistry
	registry2 *task.TaskRegistry

	localCallCount atomic.Int32
}

func (r *remotethenlocal) Setup(t *testing.T) []framework.Option {
	r.place = placement.New(t)
	r.sched = scheduler.New(t)

	app1 := app.New(t)
	app2 := app.New(t)

	r.registry1 = task.NewTaskRegistry()
	r.registry2 = task.NewTaskRegistry()

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
	)

	r.registry1.AddOrchestratorN("RemoteThenLocalWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}

		remoteTask := ctx.CallActivity("RemoteActivity",
			task.WithActivityInput(input),
			task.WithActivityAppID(r.daprd2.AppID()))
		localTask := ctx.CallActivity("LocalActivity",
			task.WithActivityInput(input))

		var remoteResult string
		if err := remoteTask.Await(&remoteResult); err != nil {
			return nil, fmt.Errorf("remote activity failed: %w", err)
		}
		var localResult string
		if err := localTask.Await(&localResult); err != nil {
			return nil, fmt.Errorf("local activity failed: %w", err)
		}
		return remoteResult + "," + localResult, nil
	})

	r.registry1.AddActivityN("LocalActivity", func(ctx task.ActivityContext) (any, error) {
		r.localCallCount.Add(1)
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		return "local:" + input, nil
	})

	r.registry2.AddActivityN("RemoteActivity", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		return "remote:" + input, nil
	})

	return []framework.Option{
		framework.WithProcesses(r.place, r.sched, app1, app2, r.daprd1),
	}
}

func (r *remotethenlocal) Run(t *testing.T, ctx context.Context) {
	r.sched.WaitUntilRunning(t, ctx)
	r.place.WaitUntilRunning(t, ctx)
	r.daprd1.WaitUntilRunning(t, ctx)

	client1 := client.NewTaskHubGrpcClient(r.daprd1.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, client1.StartWorkItemListener(ctx, r.registry1))

	id, err := client1.ScheduleNewOrchestration(ctx, "RemoteThenLocalWorkflow", api.WithInput("hello"))
	require.NoError(t, err)

	metadata, err := client1.WaitForOrchestrationStart(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_RUNNING, metadata.RuntimeStatus)

	r.daprd2.Run(t, ctx)
	t.Cleanup(func() { r.daprd2.Cleanup(t) })
	r.daprd2.WaitUntilRunning(t, ctx)

	client2 := client.NewTaskHubGrpcClient(r.daprd2.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, client2.StartWorkItemListener(ctx, r.registry2))

	metadata, err = client1.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.OrchestrationMetadataIsComplete(metadata))
	assert.Equal(t, `"remote:hello,local:hello"`, metadata.GetOutput().GetValue())
	assert.Equal(t, int32(1), r.localCallCount.Load(), "local activity should be called exactly once")
}
