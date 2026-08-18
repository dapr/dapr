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

package activityv2

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
	"github.com/dapr/dapr/tests/integration/suite/daprd/workflow/scheduler/counters"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(basic))
}

type basic struct {
	daprd     *daprd.Daprd
	place     *placement.Placement
	scheduler *procscheduler.Scheduler
}

func (a *basic) Setup(t *testing.T) []framework.Option {
	app := app.New(t)
	a.place = placement.New(t)
	a.scheduler = procscheduler.New(t)
	a.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithPlacementAddresses(a.place.Address()),
		daprd.WithInMemoryActorStateStore("statestore"),
		daprd.WithSchedulerAddresses(a.scheduler.Address()),
		daprd.WithConfigManifests(t, counters.FastPathFeatureConfig),
	)

	return []framework.Option{
		framework.WithProcesses(a.scheduler, a.place, app, a.daprd),
	}
}

func (a *basic) Run(t *testing.T, ctx context.Context) {
	a.scheduler.WaitUntilRunning(t, ctx)
	a.place.WaitUntilRunning(t, ctx)
	a.daprd.WaitUntilRunning(t, ctx)

	r := task.NewTaskRegistry()
	require.NoError(t, r.AddWorkflowN("ActivityEventActivity", func(c *task.WorkflowContext) (any, error) {
		var mid string
		if err := c.CallActivity("SayHello", task.WithActivityInput("Dapr")).Await(&mid); err != nil {
			return nil, err
		}
		if err := c.WaitForSingleEvent("go", time.Minute).Await(new([]byte)); err != nil {
			return nil, err
		}
		var out string
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

	backendClient := client.NewTaskHubGrpcClient(a.daprd.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, backendClient.StartWorkItemListener(ctx, r))

	resp, err := a.daprd.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "ActivityEventActivity",
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		janitors, newEvents := counters.JobCounts(t, ctx, a.scheduler)
		assert.Equal(c, 1, janitors, "exactly one janitor backstop while the instance runs")
		assert.Zero(c, newEvents, "wake v2 must not create per-event new-event one-shot jobs")
		assert.GreaterOrEqual(c, counters.LocalActivityStatusCount(t, ctx, a.daprd, "success"), float64(1),
			"the first activity must have been driven locally")
	}, time.Second*20, time.Millisecond*50)
	assert.Zero(t, counters.RunActivityJobCount(t, ctx, a.scheduler),
		"activity v2 must not create run-activity one-shot jobs")

	_, err = a.daprd.GRPCClient(t, ctx).RaiseEventWorkflowBeta1(ctx, &rtv1.RaiseEventWorkflowRequest{
		InstanceId:        resp.GetInstanceId(),
		WorkflowComponent: "dapr",
		EventName:         "go",
	})
	require.NoError(t, err)

	metadata, err := backendClient.WaitForWorkflowCompletion(ctx, api.InstanceID(resp.GetInstanceId()))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	assert.Equal(t, `"Hello, Hello, Dapr!!"`, metadata.GetOutput().GetValue())

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		janitors, newEvents := counters.JobCounts(t, ctx, a.scheduler)
		assert.Zero(c, janitors, "the janitor must be deleted at the terminal turn")
		assert.Zero(c, newEvents)
		assert.Zero(c, counters.RunActivityJobCount(t, ctx, a.scheduler))
	}, time.Second*60, time.Millisecond*50)

	assert.GreaterOrEqual(t, counters.LocalActivityStatusCount(t, ctx, a.daprd, "success"), float64(2))
	assert.Zero(t, counters.LocalActivityStatusCount(t, ctx, a.daprd, "janitor_redispatched"),
		"a healthy run must not need janitor re-dispatch")
}
