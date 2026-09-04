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
	suite.Register(new(pendingstartretry))
}

type pendingstartretry struct {
	daprd     *daprd.Daprd
	place     *placement.Placement
	scheduler *procscheduler.Scheduler
}

func (p *pendingstartretry) Setup(t *testing.T) []framework.Option {
	app := app.New(t)
	p.place = placement.New(t)
	p.scheduler = procscheduler.New(t)
	p.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithPlacementAddresses(p.place.Address()),
		daprd.WithInMemoryActorStateStore("statestore"),
		daprd.WithSchedulerAddresses(p.scheduler.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(p.scheduler, p.place, app, p.daprd),
	}
}

func (p *pendingstartretry) Run(t *testing.T, ctx context.Context) {
	p.scheduler.WaitUntilRunning(t, ctx)
	p.place.WaitUntilRunning(t, ctx)
	p.daprd.WaitUntilRunning(t, ctx)

	r := task.NewTaskRegistry()
	require.NoError(t, r.AddWorkflowN("PendingStart", func(c *task.WorkflowContext) (any, error) {
		var input string
		if err := c.GetInput(&input); err != nil {
			return nil, err
		}
		return "Hello, " + input + "!", nil
	}))

	backendClient := client.NewTaskHubGrpcClient(p.daprd.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, backendClient.StartWorkItemListener(ctx, r))

	const instanceID = "pending-start-retry"

	p.scheduler.Kill(t)

	gclient := p.daprd.GRPCClient(t, ctx)

	shortCtx, cancel := context.WithTimeout(ctx, time.Second*5)
	_, err := gclient.StartWorkflowBeta1(shortCtx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "PendingStart",
		InstanceId:        instanceID,
		Input:             []byte(`"Dapr"`),
	})
	cancel()
	require.Error(t, err, "the create must fail while the scheduler is unavailable")

	p.scheduler.Restart(t, ctx)
	p.scheduler.WaitUntilRunning(t, ctx)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		rctx, rcancel := context.WithTimeout(ctx, time.Second*10)
		defer rcancel()
		_, rerr := gclient.StartWorkflowBeta1(rctx, &rtv1.StartWorkflowRequest{
			WorkflowComponent: "dapr",
			WorkflowName:      "PendingStart",
			InstanceId:        instanceID,
			Input:             []byte(`"Dapr"`),
		})
		assert.NoError(c, rerr)
	}, time.Second*30, time.Millisecond*100)

	metadata, err := backendClient.WaitForWorkflowCompletion(ctx, api.InstanceID(instanceID))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	assert.Equal(t, `"Hello, Dapr!"`, metadata.GetOutput().GetValue())
}
