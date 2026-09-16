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

package schedulerplacement

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(workflowcutover))
}

// workflowcutover parks a workflow on an external event, hands the placement
// authority from the placement service to the scheduler, and completes the
// workflow: the orchestrator and activity actors survive the rehoming.
type workflowcutover struct {
	sched  *scheduler.Scheduler
	place  *placement.Placement
	daprds [2]*daprd.Daprd
}

func (w *workflowcutover) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	w.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))
	w.place = placement.New(t)
	db := sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithMetadata("busyTimeout", "10s"),
		sqlite.WithMetadata("disableWAL", "true"),
	)

	w.daprds[0] = daprd.New(t,
		daprd.WithResourceFiles(db.GetComponent(t)),
		daprd.WithScheduler(w.sched),
		daprd.WithPlacementAddresses(w.place.Address()),
	)
	w.daprds[1] = daprd.New(t,
		daprd.WithResourceFiles(db.GetComponent(t)),
		daprd.WithScheduler(w.sched),
		daprd.WithPlacementAddresses(w.place.Address()),
		daprd.WithAppID(w.daprds[0].AppID()),
	)

	return []framework.Option{
		framework.WithProcesses(w.sched, w.place, db, w.daprds[0], w.daprds[1]),
	}
}

func (w *workflowcutover) Run(t *testing.T, ctx context.Context) {
	w.sched.WaitUntilRunning(t, ctx)
	w.place.WaitUntilRunning(t, ctx)
	for _, d := range w.daprds {
		d.WaitUntilRunning(t, ctx)
	}

	registry := func() *task.TaskRegistry {
		r := task.NewTaskRegistry()
		require.NoError(t, r.AddWorkflowN("cutover", func(ctx *task.WorkflowContext) (any, error) {
			var before string
			if err := ctx.CallActivity("Before").Await(&before); err != nil {
				return nil, err
			}
			var event string
			if err := ctx.WaitForSingleEvent("go", time.Minute*3).Await(&event); err != nil {
				return nil, err
			}
			return before + event, nil
		}))
		require.NoError(t, r.AddActivityN("Before", func(ctx task.ActivityContext) (any, error) {
			return "before-", nil
		}))
		return r
	}

	clients := make([]*client.TaskHubGrpcClient, len(w.daprds))
	for i, d := range w.daprds {
		clients[i] = client.NewTaskHubGrpcClient(d.GRPCConn(t, ctx), logger.New(t))
		require.NoError(t, clients[i].StartWorkItemListener(ctx, registry()))
	}

	id, err := clients[0].ScheduleNewWorkflow(ctx, "cutover", api.WithInstanceID("cutover-wf"))
	require.NoError(t, err)

	// The workflow is parked on the external event before the authority
	// moves.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, merr := clients[0].FetchWorkflowMetadata(ctx, id)
		if !assert.NoError(c, merr) {
			return
		}
		assert.True(c, api.WorkflowMetadataIsRunning(meta))
	}, time.Second*30, time.Millisecond*50)

	w.place.Cleanup(t)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var streams float64
		for k, v := range w.sched.Metrics(c, ctx).All() {
			if strings.HasPrefix(k, "dapr_scheduler_placement_streams_connected") {
				streams += v
			}
		}
		assert.GreaterOrEqual(c, streams, float64(2))
	}, time.Second*30, time.Millisecond*100)

	require.NoError(t, clients[0].RaiseEvent(ctx, id, "go", api.WithEventPayload("after")))

	wctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	meta, err := clients[0].WaitForWorkflowCompletion(wctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(meta))
	assert.Equal(t, `"before-after"`, meta.GetOutput().GetValue())
}
