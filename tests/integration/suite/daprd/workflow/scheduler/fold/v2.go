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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	procscheduler "github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(v2))
}

type v2 struct {
	daprd     *daprd.Daprd
	place     *placement.Placement
	scheduler *procscheduler.Scheduler
}

func (f *v2) Setup(t *testing.T) []framework.Option {
	app := app.New(t)
	f.place = placement.New(t)
	f.scheduler = procscheduler.New(t)
	f.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithPlacementAddresses(f.place.Address()),
		daprd.WithInMemoryActorStateStore("statestore"),
		daprd.WithSchedulerAddresses(f.scheduler.Address()),
		daprd.WithFeatureEnabled(t, "WorkflowsFastPath"),
	)

	return []framework.Option{
		framework.WithProcesses(f.scheduler, f.place, app, f.daprd),
	}
}

func (f *v2) Run(t *testing.T, ctx context.Context) {
	f.scheduler.WaitUntilRunning(t, ctx)
	f.place.WaitUntilRunning(t, ctx)
	f.daprd.WaitUntilRunning(t, ctx)

	r := task.NewTaskRegistry()
	require.NoError(t, r.AddWorkflowN("FoldMix", func(c *task.WorkflowContext) (any, error) {
		var mid string
		if err := c.CallActivity("SayHello", task.WithActivityInput("Dapr")).Await(&mid); err != nil {
			return nil, err
		}
		if err := c.WaitForSingleEvent("go", time.Minute).Await(new([]byte)); err != nil {
			return nil, err
		}
		var out string
		if err := c.CallActivity("SayHello", task.WithActivityInput(mid)).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, r.AddActivityN("SayHello", func(c task.ActivityContext) (any, error) {
		var inp string
		if err := c.GetInput(&inp); err != nil {
			return nil, err
		}
		return fmt.Sprintf("Hello, %s!", inp), nil
	}))

	backendClient := client.NewTaskHubGrpcClient(f.daprd.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, backendClient.StartWorkItemListener(ctx, r))

	resp, err := f.daprd.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "FoldMix",
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, f.daprd.Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_completions_fold_count", "status:folded"), float64(1),
			"the activity completion must have been folded into a turn commit")
		janitors := f.scheduler.JobKeyCount(t, ctx, "new-event-janitor")
		newEvents := f.scheduler.JobKeyCount(t, ctx, "new-event") - janitors
		assert.Equal(c, 1, janitors)
		assert.Zero(c, newEvents)
	}, time.Second*20, time.Millisecond*50)
	assert.Zero(t, f.scheduler.JobKeyCount(t, ctx, "run-activity"))

	_, err = f.daprd.GRPCClient(t, ctx).RaiseEventWorkflowBeta1(ctx, &rtv1.RaiseEventWorkflowRequest{
		InstanceId:        resp.GetInstanceId(),
		WorkflowComponent: "dapr",
		EventName:         "go",
	})
	require.NoError(t, err)

	metadata, err := backendClient.WaitForWorkflowCompletion(ctx, api.InstanceID(resp.GetInstanceId()))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	assert.Equal(t, `"Hello, Hello, Dapr!!"`, metadata.GetOutput().GetValue())

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, f.daprd.Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_completions_fold_count", "status:folded"), float64(2))
		assert.GreaterOrEqual(c, f.daprd.Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_completions_fold_wait_latency_count"), float64(2),
			"every folded completion must record its commit wait")
	}, time.Second*10, time.Millisecond*50)
	assert.Zero(t, f.daprd.Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_completions_fold_count", "status:fold_nacked"),
		"a healthy run must not nack any folded completion")

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		janitors := f.scheduler.JobKeyCount(t, ctx, "new-event-janitor")
		newEvents := f.scheduler.JobKeyCount(t, ctx, "new-event") - janitors
		assert.Zero(c, janitors)
		assert.Zero(c, newEvents)
		assert.Zero(c, f.scheduler.JobKeyCount(t, ctx, "run-activity"))
	}, time.Second*60, time.Millisecond*50)
}
