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

package fold

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
	suite.Register(new(restart))
}

type restart struct {
	place     *placement.Placement
	scheduler *procscheduler.Scheduler
	db        *sqlite.SQLite
}

func (f *restart) Setup(t *testing.T) []framework.Option {
	f.place = placement.New(t)
	f.scheduler = procscheduler.New(t)
	f.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithMetadata("busyTimeout", "10s"),
		sqlite.WithMetadata("disableWAL", "true"),
	)

	return []framework.Option{
		framework.WithProcesses(f.place, f.scheduler, f.db),
	}
}

func (f *restart) Run(t *testing.T, ctx context.Context) {
	f.scheduler.WaitUntilRunning(t, ctx)
	f.place.WaitUntilRunning(t, ctx)

	appID := uuid.New().String()
	newDaprd := func() *daprd.Daprd {
		return daprd.New(t,
			daprd.WithAppID(appID),
			daprd.WithResourceFiles(f.db.GetComponent(t)),
			daprd.WithPlacementAddresses(f.place.Address()),
			daprd.WithSchedulerAddresses(f.scheduler.Address()),
			daprd.WithFeatureEnabled(t, "WorkflowsFastPath"),
			daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_WORKFLOW_JANITOR_PERIOD", "2s")),
		)
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

	daprd1 := newDaprd()
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
		janitors := f.scheduler.JobKeyCount(t, ctx, "new-event-janitor")
		assert.Equal(c, 1, janitors)
	}, time.Second*20, time.Millisecond*50)

	daprd1.Cleanup(t)

	registry2 := newRegistry(func(c task.ActivityContext) (any, error) {
		return "recovered", nil
	})

	daprd2 := newDaprd()
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
	assert.Equal(t, `"recovered"`, metadata.GetOutput().GetValue())

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, daprd2.Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_activity_count", "status:janitor_redispatched"), float64(1))
		assert.GreaterOrEqual(c, daprd2.Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_completions_fold_count", "status:folded"), float64(1),
			"the recovered activity's completion must have been folded on the new owner")
	}, time.Second*10, time.Millisecond*50)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		janitors := f.scheduler.JobKeyCount(t, ctx, "new-event-janitor")
		newEvents := f.scheduler.JobKeyCount(t, ctx, "new-event") - janitors
		assert.Zero(c, janitors)
		assert.Zero(c, newEvents)
		assert.Zero(c, f.scheduler.JobKeyCount(t, ctx, "run-activity"))
	}, time.Second*60, time.Millisecond*50)
}
