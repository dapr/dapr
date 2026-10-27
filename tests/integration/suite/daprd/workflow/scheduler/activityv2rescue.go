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

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	procscheduler "github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(activityv2rescue))
}

// activityv2rescue drops activity completion deliveries and requires the
// janitor re-dispatch chain to rescue the activity to completion.
type activityv2rescue struct {
	daprd     *daprd.Daprd
	scheduler *procscheduler.Scheduler
	place     *placement.Placement
}

func (a *activityv2rescue) Setup(t *testing.T) []framework.Option {
	a.place = placement.New(t)
	a.scheduler = procscheduler.New(t)
	db := sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithMetadata("busyTimeout", "10s"),
		sqlite.WithMetadata("disableWAL", "true"),
	)

	a.daprd = daprd.New(t,
		daprd.WithResourceFiles(db.GetComponent(t)),
		daprd.WithPlacementAddresses(a.place.Address()),
		daprd.WithSchedulerAddresses(a.scheduler.Address()),
		daprd.WithFeatureEnabled(t, "WorkflowsFastPath"),
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_WORKFLOW_JANITOR_PERIOD", "2s",
			"DAPR_WORKFLOW_TEST_DROP_ACTIVITY_COMPLETIONS", "1",
		)),
	)

	return []framework.Option{
		framework.WithProcesses(a.place, a.scheduler, db, a.daprd),
	}
}

func (a *activityv2rescue) Run(t *testing.T, ctx context.Context) {
	a.scheduler.WaitUntilRunning(t, ctx)
	a.place.WaitUntilRunning(t, ctx)
	a.daprd.WaitUntilRunning(t, ctx)

	var executions atomic.Int64
	registry := task.NewTaskRegistry()
	require.NoError(t, registry.AddWorkflowN("OneActivity", func(c *task.WorkflowContext) (any, error) {
		var out string
		if err := c.CallActivity("SayHello", task.WithActivityInput("Dapr")).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, registry.AddActivityN("SayHello", func(task.ActivityContext) (any, error) {
		executions.Add(1)
		return "rescued", nil
	}))

	cl := client.NewTaskHubGrpcClient(a.daprd.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, cl.StartWorkItemListener(ctx, registry))

	resp, err := a.daprd.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "OneActivity",
		InstanceId:        uuid.New().String(),
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), executions.Load())
	}, time.Second*20, time.Millisecond*50)
	assert.Zero(t, a.scheduler.JobKeyCount(t, ctx, "run-activity"),
		"the in-flight activity must have no durable run-activity job")

	wctx, cancel := context.WithTimeout(ctx, time.Second*30)
	defer cancel()
	metadata, err := cl.WaitForWorkflowCompletion(wctx, api.InstanceID(resp.GetInstanceId()))
	require.NoError(t, err, "the janitor did not rescue the stranded activity (janitor-livelock)")
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	assert.Equal(t, `"rescued"`, metadata.GetOutput().GetValue())

	assert.Equal(t, int64(2), executions.Load())
	assert.GreaterOrEqual(t, a.daprd.Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_activity_count", "status:claim_evicted"), float64(1),
		"the dead in-flight claim must have been evicted")
	assert.GreaterOrEqual(t, a.daprd.Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_activity_count", "status:janitor_redispatched"), float64(1),
		"the unresolved activity must have been re-dispatched by the janitor")

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		janitors := a.scheduler.JobKeyCount(t, ctx, "new-event-janitor")
		newEvents := a.scheduler.JobKeyCount(t, ctx, "new-event") - janitors
		assert.Zero(c, janitors)
		assert.Zero(c, newEvents)
		assert.Zero(c, a.scheduler.JobKeyCount(t, ctx, "run-activity"))
	}, time.Second*60, time.Millisecond*50)
}
