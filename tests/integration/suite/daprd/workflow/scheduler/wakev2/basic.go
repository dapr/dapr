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
	suite.Register(new(basic))
}

type basic struct {
	daprd     *daprd.Daprd
	place     *placement.Placement
	scheduler *procscheduler.Scheduler
}

func (w *basic) Setup(t *testing.T) []framework.Option {
	app := app.New(t)
	w.place = placement.New(t)
	w.scheduler = procscheduler.New(t)
	w.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithPlacementAddresses(w.place.Address()),
		daprd.WithInMemoryActorStateStore("statestore"),
		daprd.WithSchedulerAddresses(w.scheduler.Address()),
		daprd.WithFeatureEnabled(t, "WorkflowsFastPath"),
	)

	return []framework.Option{
		framework.WithProcesses(w.scheduler, w.place, app, w.daprd),
	}
}

func (w *basic) Run(t *testing.T, ctx context.Context) {
	w.scheduler.WaitUntilRunning(t, ctx)
	w.place.WaitUntilRunning(t, ctx)
	w.daprd.WaitUntilRunning(t, ctx)

	r := task.NewTaskRegistry()
	require.NoError(t, r.AddWorkflowN("ActivityThenEvent", func(c *task.WorkflowContext) (any, error) {
		var out string
		if err := c.CallActivity("SayHello", task.WithActivityInput("Dapr")).Await(&out); err != nil {
			return nil, err
		}
		if err := c.WaitForSingleEvent("go", time.Minute).Await(new([]byte)); err != nil {
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

	backendClient := client.NewTaskHubGrpcClient(w.daprd.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, backendClient.StartWorkItemListener(ctx, r))

	resp, err := w.daprd.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "ActivityThenEvent",
		Input:             []byte(`"Dapr"`),
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		janitors := w.scheduler.JobKeyCount(t, ctx, "new-event-janitor")
		newEvents := w.scheduler.JobKeyCount(t, ctx, "new-event") - janitors
		assert.Equal(c, 1, janitors, "exactly one janitor backstop while the instance runs")
		assert.Zero(c, newEvents, "wake v2 must not create per-event new-event one-shot jobs")
	}, time.Second*20, time.Millisecond*50)

	_, err = w.daprd.GRPCClient(t, ctx).RaiseEventWorkflowBeta1(ctx, &rtv1.RaiseEventWorkflowRequest{
		InstanceId:        resp.GetInstanceId(),
		WorkflowComponent: "dapr",
		EventName:         "go",
	})
	require.NoError(t, err)

	metadata, err := backendClient.WaitForWorkflowCompletion(ctx, api.InstanceID(resp.GetInstanceId()))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		janitors := w.scheduler.JobKeyCount(t, ctx, "new-event-janitor")
		newEvents := w.scheduler.JobKeyCount(t, ctx, "new-event") - janitors
		assert.Zero(c, janitors, "the janitor must be deleted at the terminal turn")
		assert.Zero(c, newEvents)
	}, time.Second*60, time.Millisecond*50)

	assert.GreaterOrEqual(t, w.daprd.Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_wake_count", "status:success"), float64(3))
}
