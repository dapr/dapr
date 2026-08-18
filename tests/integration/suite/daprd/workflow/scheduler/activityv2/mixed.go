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
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	procscheduler "github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(mixed))
}

type mixed struct {
	place     *placement.Placement
	scheduler *procscheduler.Scheduler
	db        *sqlite.SQLite
}

func (a *mixed) Setup(t *testing.T) []framework.Option {
	a.place = placement.New(t)
	a.scheduler = procscheduler.New(t)
	a.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithMetadata("busyTimeout", "10s"),
		sqlite.WithMetadata("disableWAL", "true"),
	)

	return []framework.Option{
		framework.WithProcesses(a.place, a.scheduler, a.db),
	}
}

func (a *mixed) Run(t *testing.T, ctx context.Context) {
	a.scheduler.WaitUntilRunning(t, ctx)
	a.place.WaitUntilRunning(t, ctx)

	appID := uuid.New().String()
	newDaprd := func(gateOn bool) *daprd.Daprd {
		opts := []daprd.Option{
			daprd.WithAppID(appID),
			daprd.WithResourceFiles(a.db.GetComponent(t)),
			daprd.WithPlacementAddresses(a.place.Address()),
			daprd.WithSchedulerAddresses(a.scheduler.Address()),
			daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_WORKFLOW_JANITOR_PERIOD", "2s")),
		}
		if gateOn {
			opts = append(opts, daprd.WithFeatureEnabled(t, "WorkflowsFastPath"))
		}
		return daprd.New(t, opts...)
	}

	newRegistry := func(activity func(task.ActivityContext) (any, error)) *task.TaskRegistry {
		r := task.NewTaskRegistry()
		require.NoError(t, r.AddWorkflowN("OneActivity", func(c *task.WorkflowContext) (any, error) {
			var out string
			if err := c.CallActivity("SayHello", task.WithActivityInput("Dapr")).Await(&out); err != nil {
				return nil, err
			}
			return out, nil
		}))
		require.NoError(t, r.AddActivityN("SayHello", activity))
		return r
	}

	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	started := make(chan struct{}, 1)
	registry1 := newRegistry(func(task.ActivityContext) (any, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-block
		return nil, nil
	})

	daprd1 := newDaprd(true)
	daprd1.Run(t, ctx)
	daprd1.WaitUntilRunning(t, ctx)

	client1 := client.NewTaskHubGrpcClient(daprd1.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, client1.StartWorkItemListener(ctx, registry1))

	resp, err := daprd1.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "OneActivity",
	})
	require.NoError(t, err)

	select {
	case <-started:
	case <-time.After(time.Second * 20):
		require.Fail(t, "timed out waiting for the activity to start executing")
	}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, merr := client1.FetchWorkflowMetadata(ctx, api.InstanceID(resp.GetInstanceId()))
		if assert.NoError(c, merr) {
			assert.Equal(c, "ORCHESTRATION_STATUS_RUNNING", meta.GetRuntimeStatus().String())
		}
		janitors := a.scheduler.JobKeyCount(t, ctx, "new-event-janitor")
		assert.Equal(c, 1, janitors)
	}, time.Second*20, time.Millisecond*50)
	assert.Zero(t, a.scheduler.JobKeyCount(t, ctx, "run-activity"))

	daprd1.Cleanup(t)

	registry2 := newRegistry(func(c task.ActivityContext) (any, error) {
		return "recovered-off", nil
	})

	daprd2 := newDaprd(false)
	daprd2.Run(t, ctx)
	t.Cleanup(func() { daprd2.Cleanup(t) })
	daprd2.WaitUntilRunning(t, ctx)

	client2 := client.NewTaskHubGrpcClient(daprd2.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, client2.StartWorkItemListener(ctx, registry2))

	wctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	metadata, err := client2.WaitForWorkflowCompletion(wctx, api.InstanceID(resp.GetInstanceId()))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	assert.Equal(t, `"recovered-off"`, metadata.GetOutput().GetValue())

	assert.GreaterOrEqual(t, daprd2.Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_activity_count", "status:janitor_redispatched"), float64(1),
		"the gate-off host must still run the unconditional janitor re-dispatch")

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		janitors := a.scheduler.JobKeyCount(t, ctx, "new-event-janitor")
		newEvents := a.scheduler.JobKeyCount(t, ctx, "new-event") - janitors
		assert.Zero(c, janitors)
		assert.Zero(c, newEvents)
		assert.Zero(c, a.scheduler.JobKeyCount(t, ctx, "run-activity"))
	}, time.Second*60, time.Millisecond*50)
}
