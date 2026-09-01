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
	suite.Register(new(restart))
}

type restart struct {
	place     *placement.Placement
	scheduler *procscheduler.Scheduler
	db        *sqlite.SQLite
}

func (w *restart) Setup(t *testing.T) []framework.Option {
	w.place = placement.New(t)
	w.scheduler = procscheduler.New(t)
	w.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithMetadata("busyTimeout", "10s"),
		sqlite.WithMetadata("disableWAL", "true"),
	)

	return []framework.Option{
		framework.WithProcesses(w.place, w.scheduler, w.db),
	}
}

func (w *restart) Run(t *testing.T, ctx context.Context) {
	w.scheduler.WaitUntilRunning(t, ctx)
	w.place.WaitUntilRunning(t, ctx)

	appID := uuid.New().String()
	newDaprd := func() *daprd.Daprd {
		return daprd.New(t,
			daprd.WithAppID(appID),
			daprd.WithResourceFiles(w.db.GetComponent(t)),
			daprd.WithPlacementAddresses(w.place.Address()),
			daprd.WithSchedulerAddresses(w.scheduler.Address()),
			daprd.WithFeatureEnabled(t, "WorkflowsFastPath"),
			daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_WORKFLOW_JANITOR_PERIOD", "2s")),
		)
	}

	registry := task.NewTaskRegistry()
	require.NoError(t, registry.AddWorkflowN("WaitForGo", func(c *task.WorkflowContext) (any, error) {
		if err := c.WaitForSingleEvent("go", time.Minute*3).Await(new([]byte)); err != nil {
			return nil, err
		}
		return "done", nil
	}))

	// daprd1's app blocks the resumed turn forever, so the raised event can
	// never commit before the kill: the recovery MUST come from daprd2's
	// janitor rather than racing daprd1's local drive against the shutdown.
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	registry1 := task.NewTaskRegistry()
	require.NoError(t, registry1.AddWorkflowN("WaitForGo", func(c *task.WorkflowContext) (any, error) {
		if err := c.WaitForSingleEvent("go", time.Minute*3).Await(new([]byte)); err != nil {
			return nil, err
		}
		<-block
		return "done", nil
	}))

	daprd1 := newDaprd()
	daprd1.Run(t, ctx)
	daprd1.WaitUntilRunning(t, ctx)

	client1 := client.NewTaskHubGrpcClient(daprd1.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, client1.StartWorkItemListener(ctx, registry1))

	resp, err := daprd1.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "WaitForGo",
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, merr := client1.FetchWorkflowMetadata(ctx, api.InstanceID(resp.GetInstanceId()))
		if assert.NoError(c, merr) {
			assert.Equal(c, "ORCHESTRATION_STATUS_RUNNING", meta.GetRuntimeStatus().String())
		}
	}, time.Second*20, time.Millisecond*50)

	_, err = daprd1.GRPCClient(t, ctx).RaiseEventWorkflowBeta1(ctx, &rtv1.RaiseEventWorkflowRequest{
		InstanceId:        resp.GetInstanceId(),
		WorkflowComponent: "dapr",
		EventName:         "go",
	})
	require.NoError(t, err)
	daprd1.Kill(t)

	daprd2 := newDaprd()
	daprd2.Run(t, ctx)
	t.Cleanup(func() { daprd2.Cleanup(t) })
	daprd2.WaitUntilRunning(t, ctx)

	client2 := client.NewTaskHubGrpcClient(daprd2.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, client2.StartWorkItemListener(ctx, registry))

	wctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	metadata, err := client2.WaitForWorkflowCompletion(wctx, api.InstanceID(resp.GetInstanceId()))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	assert.Positive(t, daprd2.Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_wake_count", "status:janitor_recovered"),
		"the restart recovery must be attributed to the janitor")

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		janitors := w.scheduler.JobKeyCount(t, ctx, "new-event-janitor")
		newEvents := w.scheduler.JobKeyCount(t, ctx, "new-event") - janitors
		assert.Zero(c, janitors)
		assert.Zero(c, newEvents)
	}, time.Second*60, time.Millisecond*50)
}
