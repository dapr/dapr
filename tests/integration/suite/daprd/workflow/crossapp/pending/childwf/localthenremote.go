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

package childwf

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
	suite.Register(new(localthenremote))
}

type localthenremote struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	place  *placement.Placement
	sched  *scheduler.Scheduler

	registry1 *task.TaskRegistry
	registry2 *task.TaskRegistry

	localChildCount atomic.Int32
}

func (l *localthenremote) Setup(t *testing.T) []framework.Option {
	l.place = placement.New(t)
	l.sched = scheduler.New(t)

	app1 := app.New(t)
	app2 := app.New(t)

	l.registry1 = task.NewTaskRegistry()
	l.registry2 = task.NewTaskRegistry()

	l.daprd1 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(l.place.Address()),
		daprd.WithScheduler(l.sched),
		daprd.WithAppPort(app1.Port()),
		daprd.WithLogLevel("debug"),
	)
	l.daprd2 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(l.place.Address()),
		daprd.WithScheduler(l.sched),
		daprd.WithAppPort(app2.Port()),
		daprd.WithLogLevel("debug"),
	)

	l.registry1.AddWorkflowN("ParentWorkflow", func(ctx *task.WorkflowContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}

		localTask := ctx.CallChildWorkflow("LocalChild",
			task.WithChildWorkflowInput(input))
		remoteTask := ctx.CallChildWorkflow("RemoteChild",
			task.WithChildWorkflowInput(input),
			task.WithChildWorkflowAppID(l.daprd2.AppID()))

		var localResult string
		if err := localTask.Await(&localResult); err != nil {
			return nil, fmt.Errorf("local child failed: %w", err)
		}
		var remoteResult string
		if err := remoteTask.Await(&remoteResult); err != nil {
			return nil, fmt.Errorf("remote child failed: %w", err)
		}
		return localResult + "," + remoteResult, nil
	})

	l.registry1.AddWorkflowN("LocalChild", func(ctx *task.WorkflowContext) (any, error) {
		l.localChildCount.Add(1)
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		return "local:" + input, nil
	})

	l.registry2.AddWorkflowN("RemoteChild", func(ctx *task.WorkflowContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		return "remote:" + input, nil
	})

	return []framework.Option{
		framework.WithProcesses(l.place, l.sched, app1, app2, l.daprd1),
	}
}

func (l *localthenremote) Run(t *testing.T, ctx context.Context) {
	l.sched.WaitUntilRunning(t, ctx)
	l.place.WaitUntilRunning(t, ctx)
	l.daprd1.WaitUntilRunning(t, ctx)

	client1 := client.NewTaskHubGrpcClient(l.daprd1.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, client1.StartWorkItemListener(ctx, l.registry1))

	id, err := client1.ScheduleNewWorkflow(ctx, "ParentWorkflow", api.WithInput("hello"))
	require.NoError(t, err)

	metadata, err := client1.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_RUNNING, metadata.GetRuntimeStatus())

	l.daprd2.Run(t, ctx)
	t.Cleanup(func() { l.daprd2.Cleanup(t) })
	l.daprd2.WaitUntilRunning(t, ctx)

	client2 := client.NewTaskHubGrpcClient(l.daprd2.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, client2.StartWorkItemListener(ctx, l.registry2))

	metadata, err = client1.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	assert.Equal(t, `"local:hello,remote:hello"`, metadata.GetOutput().GetValue())
	assert.Equal(t, int32(1), l.localChildCount.Load(), "local child workflow should be called exactly once")
}
