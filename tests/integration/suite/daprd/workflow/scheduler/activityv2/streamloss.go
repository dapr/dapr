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
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

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
	suite.Register(new(streamloss))
}

// streamloss severs the app work-item stream mid-activity and asserts the
// janitor re-drives the activity on a reconnected worker.
type streamloss struct {
	daprd     *daprd.Daprd
	scheduler *procscheduler.Scheduler
	place     *placement.Placement
}

func (a *streamloss) Setup(t *testing.T) []framework.Option {
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
		)),
	)

	return []framework.Option{
		framework.WithProcesses(a.place, a.scheduler, db, a.daprd),
	}
}

func (a *streamloss) Run(t *testing.T, ctx context.Context) {
	a.scheduler.WaitUntilRunning(t, ctx)
	a.place.WaitUntilRunning(t, ctx)
	a.daprd.WaitUntilRunning(t, ctx)

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

	dial := func() *grpc.ClientConn {
		//nolint:staticcheck
		conn, err := grpc.DialContext(ctx, a.daprd.GRPCAddress(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			//nolint:staticcheck
			grpc.WithBlock(),
			grpc.WithDefaultCallOptions(
				grpc.MaxCallRecvMsgSize(math.MaxInt32),
				grpc.MaxCallSendMsgSize(math.MaxInt32),
			),
		)
		require.NoError(t, err)
		return conn
	}

	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	started := make(chan struct{}, 1)
	conn1 := dial()
	t.Cleanup(func() { _ = conn1.Close() })

	lctx, lcancel := context.WithCancel(ctx)
	t.Cleanup(lcancel)
	client1 := client.NewTaskHubGrpcClient(conn1, backend.DefaultLogger())
	require.NoError(t, client1.StartWorkItemListener(lctx, newRegistry(func(task.ActivityContext) (any, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-block
		return nil, nil
	})))

	resp, err := a.daprd.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
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
		assert.Equal(c, 1, janitors, "the janitor must be armed before the stream break")
	}, time.Second*20, time.Millisecond*50)
	assert.Zero(t, a.scheduler.JobKeyCount(t, ctx, "run-activity"),
		"the in-flight activity must have no durable run-activity job")

	lcancel()
	require.NoError(t, conn1.Close())

	conn2 := dial()
	t.Cleanup(func() { _ = conn2.Close() })
	client2 := client.NewTaskHubGrpcClient(conn2, backend.DefaultLogger())
	require.NoError(t, client2.StartWorkItemListener(ctx, newRegistry(func(task.ActivityContext) (any, error) {
		return "recovered", nil
	})))

	wctx, cancel := context.WithTimeout(ctx, time.Second*60)
	defer cancel()
	metadata, err := client2.WaitForWorkflowCompletion(wctx, api.InstanceID(resp.GetInstanceId()))
	require.NoError(t, err, "the workflow stranded after losing its activity work item to a stream break (janitor-livelock)")
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	assert.Equal(t, `"recovered"`, metadata.GetOutput().GetValue())

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		janitors := a.scheduler.JobKeyCount(t, ctx, "new-event-janitor")
		newEvents := a.scheduler.JobKeyCount(t, ctx, "new-event") - janitors
		assert.Zero(c, janitors)
		assert.Zero(c, newEvents)
		assert.Zero(c, a.scheduler.JobKeyCount(t, ctx, "run-activity"))
	}, time.Second*60, time.Millisecond*50)
}
