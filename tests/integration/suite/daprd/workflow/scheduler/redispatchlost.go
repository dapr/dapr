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

package scheduler

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
	suite.Register(new(redispatchlost))
}

// redispatchlost drops every local activity drive arm and asserts the
// janitor escalates to the durable run-activity reminder.
type redispatchlost struct {
	daprd     *daprd.Daprd
	place     *placement.Placement
	scheduler *procscheduler.Scheduler
}

func (r *redispatchlost) Setup(t *testing.T) []framework.Option {
	app := app.New(t)
	r.place = placement.New(t)
	r.scheduler = procscheduler.New(t)
	r.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithInMemoryActorStateStore("statestore"),
		daprd.WithSchedulerAddresses(r.scheduler.Address()),
		daprd.WithConfigManifests(t, counters.FastPathFeatureConfig),
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_WORKFLOW_JANITOR_PERIOD", "2s",
			"DAPR_WORKFLOW_TEST_DROP_ACTIVITY_DRIVES", "1000",
		)),
	)

	return []framework.Option{
		framework.WithProcesses(r.scheduler, r.place, app, r.daprd),
	}
}

func (r *redispatchlost) Run(t *testing.T, ctx context.Context) {
	r.scheduler.WaitUntilRunning(t, ctx)
	r.place.WaitUntilRunning(t, ctx)
	r.daprd.WaitUntilRunning(t, ctx)

	for _, m := range []struct{ metric, status string }{
		{"local_wake", "janitor_fold_recovered"},
		{"local_wake", "janitor_recovered"},
		{"local_activity", "janitor_redispatched"},
		{"local_activity", "janitor_redispatch_escalated"},
		{"local_activity", "claim_evicted"},
	} {
		series := r.daprd.Metrics(t, ctx).MatchMetric(m.metric, m.status)
		if assert.NotEmpty(t, series, "series %s status %s must be registered at boot", m.metric, m.status) {
			assert.Zero(t, series[0].Value)
		}
	}

	var executions atomic.Int64

	registry := task.NewTaskRegistry()
	require.NoError(t, registry.AddWorkflowN("Seq", func(c *task.WorkflowContext) (any, error) {
		var out string
		for range 3 {
			if err := c.CallActivity("Echo", task.WithActivityInput("x")).Await(&out); err != nil {
				return nil, err
			}
		}
		return out, nil
	}))
	require.NoError(t, registry.AddActivityN("Echo", func(task.ActivityContext) (any, error) {
		executions.Add(1)
		return "ok", nil
	}))

	cl := client.NewTaskHubGrpcClient(r.daprd.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, cl.StartWorkItemListener(ctx, registry))

	resp, err := r.daprd.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "Seq",
	})
	require.NoError(t, err)

	wctx, cancel := context.WithTimeout(ctx, time.Second*60)
	defer cancel()
	metadata, err := cl.WaitForWorkflowCompletion(wctx, api.InstanceID(resp.GetInstanceId()))
	require.NoError(t, err, "workflow stranded on a lost activity work item (janitor re-dispatch never escalated to a durable re-driver)")
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	assert.Equal(t, `"ok"`, metadata.GetOutput().GetValue())

	assert.GreaterOrEqual(t, counters.LocalActivityStatusCount(t, ctx, r.daprd, "janitor_redispatch_escalated"), float64(3))
	assert.Equal(t, int64(3), executions.Load())

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		janitors, newEvents := counters.JobCounts(t, ctx, r.scheduler)
		assert.Zero(c, janitors, "the janitor must be deleted at the terminal turn")
		assert.Zero(c, newEvents)
		assert.Zero(c, counters.RunActivityJobCount(t, ctx, r.scheduler))
	}, time.Second*60, time.Millisecond*50)
}
