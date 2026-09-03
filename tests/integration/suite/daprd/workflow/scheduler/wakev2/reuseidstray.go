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

package wakev2

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	suite.Register(new(reuseidstray))
}

type reuseidstray struct {
	daprd     *daprd.Daprd
	place     *placement.Placement
	scheduler *procscheduler.Scheduler
}

func (r *reuseidstray) Setup(t *testing.T) []framework.Option {
	r.place = placement.New(t)
	r.scheduler = procscheduler.New(t)
	db := sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithMetadata("busyTimeout", "10s"),
		sqlite.WithMetadata("disableWAL", "true"),
	)

	r.daprd = daprd.New(t,
		daprd.WithResourceFiles(db.GetComponent(t)),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithSchedulerAddresses(r.scheduler.Address()),
		daprd.WithFeatureEnabled(t, "WorkflowsFastPath"),
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_WORKFLOW_JANITOR_PERIOD", "2s")),
	)

	return []framework.Option{
		framework.WithProcesses(r.place, r.scheduler, db, r.daprd),
	}
}

func (r *reuseidstray) Run(t *testing.T, ctx context.Context) {
	r.scheduler.WaitUntilRunning(t, ctx)
	r.place.WaitUntilRunning(t, ctx)
	r.daprd.WaitUntilRunning(t, ctx)

	const parentID = "wakev2-reuseidstray"
	const pinnedID = parentID + "-pinned"

	var inActivity atomic.Bool
	var childFailed atomic.Bool
	releaseCh := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-releaseCh:
		default:
			close(releaseCh)
		}
	})

	registry := task.NewTaskRegistry()

	require.NoError(t, registry.AddWorkflowN("occupant", func(octx *task.WorkflowContext) (any, error) {
		return nil, octx.CallActivity("block").Await(nil)
	}))
	require.NoError(t, registry.AddActivityN("block", func(task.ActivityContext) (any, error) {
		inActivity.Store(true)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-releaseCh:
			return nil, nil
		}
	}))
	require.NoError(t, registry.AddWorkflowN("quick", func(*task.WorkflowContext) (any, error) {
		return nil, nil
	}))
	require.NoError(t, registry.AddWorkflowN("parent", func(octx *task.WorkflowContext) (any, error) {
		return nil, octx.CallChildWorkflow("quick",
			task.WithChildWorkflowInstanceID(pinnedID),
			task.WithChildWorkflowRetryPolicy(&task.RetryPolicy{
				MaxAttempts:          20,
				InitialRetryInterval: time.Millisecond * 500,
				Handle: func(err error) bool {
					childFailed.Store(true)
					return true
				},
			}),
		).Await(nil)
	}))

	cl := client.NewTaskHubGrpcClient(r.daprd.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, cl.StartWorkItemListener(ctx, registry))

	_, err := cl.ScheduleNewWorkflow(ctx, "occupant", api.WithInstanceID(pinnedID))
	require.NoError(t, err)
	require.Eventually(t, inActivity.Load, time.Second*10, time.Millisecond*10)

	_, err = cl.ScheduleNewWorkflow(ctx, "parent", api.WithInstanceID(parentID))
	require.NoError(t, err)

	require.Eventually(t, childFailed.Load, time.Second*10, time.Millisecond*10,
		"the occupied-ID creation must fault the retry attempt")
	close(releaseCh)

	waitCtx, cancel := context.WithTimeout(ctx, time.Second*90)
	t.Cleanup(cancel)
	meta, err := cl.WaitForWorkflowCompletion(waitCtx, parentID)
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), meta.GetRuntimeStatus().String())

	deadline := time.Now().Add(time.Second * 8)
	for time.Now().Before(deadline) {
		ometa, oerr := cl.FetchWorkflowMetadata(ctx, api.InstanceID(pinnedID))
		require.NoError(t, oerr)
		assert.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), ometa.GetRuntimeStatus().String(),
			"the reused instance's terminal state was reset (stray start reminder)")
		assert.Equal(t, "quick", ometa.GetName())
		if t.Failed() {
			return
		}
		time.Sleep(time.Millisecond * 250)
	}
}
