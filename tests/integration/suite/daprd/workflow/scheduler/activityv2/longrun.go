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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
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
	suite.Register(new(longrun))
}

type longrun struct {
	daprd     *daprd.Daprd
	place     *placement.Placement
	scheduler *procscheduler.Scheduler
}

func (a *longrun) Setup(t *testing.T) []framework.Option {
	app := app.New(t)
	a.place = placement.New(t)
	a.scheduler = procscheduler.New(t)
	a.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithPlacementAddresses(a.place.Address()),
		daprd.WithInMemoryActorStateStore("statestore"),
		daprd.WithSchedulerAddresses(a.scheduler.Address()),
		daprd.WithConfigManifests(t, counters.FastPathFeatureConfig),
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_WORKFLOW_JANITOR_PERIOD", "1s")),
	)

	return []framework.Option{
		framework.WithProcesses(a.scheduler, a.place, app, a.daprd),
	}
}

func (a *longrun) Run(t *testing.T, ctx context.Context) {
	a.scheduler.WaitUntilRunning(t, ctx)
	a.place.WaitUntilRunning(t, ctx)
	a.daprd.WaitUntilRunning(t, ctx)

	var executions atomic.Int64

	r := task.NewTaskRegistry()
	require.NoError(t, r.AddWorkflowN("SlowActivity", func(c *task.WorkflowContext) (any, error) {
		var out string
		if err := c.CallActivity("Slow", task.WithActivityInput("Dapr")).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, r.AddActivityN("Slow", func(c task.ActivityContext) (any, error) {
		executions.Add(1)
		time.Sleep(time.Second * 4)
		return "slow-done", nil
	}))

	backendClient := client.NewTaskHubGrpcClient(a.daprd.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, backendClient.StartWorkItemListener(ctx, r))

	resp, err := a.daprd.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "SlowActivity",
	})
	require.NoError(t, err)

	metadata, err := backendClient.WaitForWorkflowCompletion(ctx, api.InstanceID(resp.GetInstanceId()))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	assert.Equal(t, `"slow-done"`, metadata.GetOutput().GetValue())

	assert.Equal(t, int64(1), executions.Load(),
		"janitor re-dispatches during the run must be absorbed by the in-flight execution, not re-run the app")

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		janitors, newEvents := counters.JobCounts(t, ctx, a.scheduler)
		assert.Zero(c, janitors)
		assert.Zero(c, newEvents)
		assert.Zero(c, counters.RunActivityJobCount(t, ctx, a.scheduler))
	}, time.Second*60, time.Millisecond*50)
}
