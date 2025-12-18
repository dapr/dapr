/*
Copyright 2025 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://wwb.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package workflow

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(delstate))
}

type delstate struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	place  *placement.Placement
	sched  *scheduler.Scheduler
}

func (d *delstate) Setup(t *testing.T) []framework.Option {
	d.sched = scheduler.New(t)
	d.place = placement.New(t)
	d.daprd1 = daprd.New(t,
		daprd.WithPlacementAddresses(d.place.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithSchedulerAddresses(d.sched.Address()),
	)
	d.daprd2 = daprd.New(t,
		daprd.WithPlacementAddresses(d.place.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithSchedulerAddresses(d.sched.Address()),
		daprd.WithAppID(d.daprd1.AppID()),
	)

	return []framework.Option{
		framework.WithProcesses(d.place, d.sched, d.daprd1),
	}
}

func (d *delstate) Run(t *testing.T, ctx context.Context) {
	d.sched.WaitUntilRunning(t, ctx)
	d.place.WaitUntilRunning(t, ctx)
	d.daprd1.WaitUntilRunning(t, ctx)

	releaseCh := make(chan struct{})
	var here atomic.Int64
	reg := workflow.NewRegistry()
	reg.AddWorkflowN("foo", func(ctx *workflow.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity("bar").Await(nil)
	})
	reg.AddActivityN("bar", func(ctx workflow.ActivityContext) (any, error) {
		here.Add(1)
		<-releaseCh
		return nil, nil
	})

	cl := workflow.NewClient(d.daprd1.GRPCConn(t, ctx))
	require.NoError(t, cl.StartWorker(ctx, reg))
	_, err := cl.ScheduleWorkflow(ctx, "foo")
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), here.Load())
	}, time.Second*10, time.Millisecond*10)

	d.daprd1.Kill(t)
	d.daprd2.Run(t, ctx)
	d.daprd2.WaitUntilRunning(t, ctx)
	t.Cleanup(func() {
		d.daprd2.Kill(t)
	})

	cl = workflow.NewClient(d.daprd2.GRPCConn(t, ctx))
	require.NoError(t, cl.StartWorker(ctx, reg))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, d.daprd2.GetMetadata(t, ctx).ActorRuntime.ActiveActors, 3)
		assert.GreaterOrEqual(c, here.Load(), int64(2))
	}, time.Second*10, time.Millisecond*10)

	time.Sleep(time.Second * 3)

	close(releaseCh)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		v := d.daprd2.Metrics(t, ctx).All()[fmt.Sprintf(
			"dapr_runtime_actor_reminders_fired_total|actor_type:dapr.internal.default.%s.activity|app_id:%s|success:true",
			d.daprd2.AppID(), d.daprd2.AppID())]
		assert.InDelta(c, 1.0, v, 0)

		v = d.daprd2.Metrics(t, ctx).All()[fmt.Sprintf(
			"dapr_runtime_workflow_activity_execution_count|activity_name:bar|app_id:%s|namespace:|status:failed",
			d.daprd2.AppID())]
		assert.InDelta(c, 1.0, v, 0)
	}, time.Second*30, time.Millisecond*10)
}
