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

package localwake

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	procscheduler "github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(basic))
	suite.Register(new(localwakeoff))
}

type basic struct {
	daprd     *daprd.Daprd
	place     *placement.Placement
	scheduler *procscheduler.Scheduler
}

func (l *basic) Setup(t *testing.T) []framework.Option {
	app := app.New(t)
	l.place = placement.New(t)
	l.scheduler = procscheduler.New(t)
	l.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithPlacementAddresses(l.place.Address()),
		daprd.WithInMemoryActorStateStore("statestore"),
		daprd.WithSchedulerAddresses(l.scheduler.Address()),
		daprd.WithFeatureEnabled(t, "WorkflowsFastPath"),
	)

	return []framework.Option{
		framework.WithProcesses(l.scheduler, l.place, app, l.daprd),
	}
}

func (l *basic) Run(t *testing.T, ctx context.Context) {
	l.scheduler.WaitUntilRunning(t, ctx)
	l.place.WaitUntilRunning(t, ctx)
	l.daprd.WaitUntilRunning(t, ctx)

	r := task.NewTaskRegistry()
	require.NoError(t, r.AddWorkflowN("TwoActivities", func(c *task.WorkflowContext) (any, error) {
		var input string
		if err := c.GetInput(&input); err != nil {
			return nil, err
		}
		var mid, out string
		if err := c.CallActivity("SayHello", task.WithActivityInput(input)).Await(&mid); err != nil {
			return nil, err
		}
		if err := c.CallActivity("SayHello", task.WithActivityInput(mid)).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, r.AddActivityN("SayHello", func(c task.ActivityContext) (any, error) {
		var inp string
		if err := c.GetInput(&inp); err != nil {
			return nil, err
		}
		return fmt.Sprintf("Hello, %s!", inp), nil
	}))

	backendClient := client.NewTaskHubGrpcClient(l.daprd.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, backendClient.StartWorkItemListener(ctx, r))

	resp, err := l.daprd.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "TwoActivities",
		Input:             []byte(`"Dapr"`),
	})
	require.NoError(t, err)

	metadata, err := backendClient.WaitForWorkflowCompletion(ctx, api.InstanceID(resp.GetInstanceId()))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	assert.Equal(t, `"Hello, Hello, Dapr!!"`, metadata.GetOutput().GetValue())

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, l.daprd.Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_wake_count", "status:success"), float64(3),
			"the start and new-event wake-ups must be driven by the local fast path")
		assert.GreaterOrEqual(c, l.daprd.Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_wake_drive_latency_count", "status:success"), float64(3),
			"every local drive must record its latency")
	}, time.Second*20, time.Millisecond*10)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, l.scheduler.ListAllKeys(t, ctx, "dapr/jobs"))
	}, time.Second*60, time.Millisecond*10)
}

type localwakeoff struct {
	daprd     *daprd.Daprd
	place     *placement.Placement
	scheduler *procscheduler.Scheduler
}

func (l *localwakeoff) Setup(t *testing.T) []framework.Option {
	app := app.New(t)
	l.place = placement.New(t)
	l.scheduler = procscheduler.New(t)
	l.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithPlacementAddresses(l.place.Address()),
		daprd.WithInMemoryActorStateStore("statestore"),
		daprd.WithSchedulerAddresses(l.scheduler.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(l.scheduler, l.place, app, l.daprd),
	}
}

func (l *localwakeoff) Run(t *testing.T, ctx context.Context) {
	l.scheduler.WaitUntilRunning(t, ctx)
	l.place.WaitUntilRunning(t, ctx)
	l.daprd.WaitUntilRunning(t, ctx)

	r := task.NewTaskRegistry()
	require.NoError(t, r.AddWorkflowN("SingleActivity", func(c *task.WorkflowContext) (any, error) {
		var input string
		if err := c.GetInput(&input); err != nil {
			return nil, err
		}
		var out string
		err := c.CallActivity("SayHello", task.WithActivityInput(input)).Await(&out)
		return out, err
	}))
	require.NoError(t, r.AddActivityN("SayHello", func(c task.ActivityContext) (any, error) {
		var inp string
		if err := c.GetInput(&inp); err != nil {
			return nil, err
		}
		return fmt.Sprintf("Hello, %s!", inp), nil
	}))

	backendClient := client.NewTaskHubGrpcClient(l.daprd.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, backendClient.StartWorkItemListener(ctx, r))

	resp, err := l.daprd.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "SingleActivity",
		Input:             []byte(`"Dapr"`),
	})
	require.NoError(t, err)

	metadata, err := backendClient.WaitForWorkflowCompletion(ctx, api.InstanceID(resp.GetInstanceId()))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))

	assert.Zero(t, l.daprd.Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_wake_count", "status:success"),
		"without the feature no wake-up may be driven locally")
}
