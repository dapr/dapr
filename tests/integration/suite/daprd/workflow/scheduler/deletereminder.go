/*
Copyright 2024 The Dapr Authors
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

package scheduler

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
	suite.Register(new(deletereminder))
}

type deletereminder struct {
	daprd     *daprd.Daprd
	place     *placement.Placement
	scheduler *procscheduler.Scheduler
}

func (d *deletereminder) Setup(t *testing.T) []framework.Option {
	app := app.New(t)
	d.place = placement.New(t)
	d.scheduler = procscheduler.New(t)
	d.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithPlacementAddresses(d.place.Address()),
		daprd.WithInMemoryActorStateStore("statestore"),
		daprd.WithSchedulerAddresses(d.scheduler.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(d.scheduler, d.place, app, d.daprd),
	}
}

func (d *deletereminder) Run(t *testing.T, ctx context.Context) {
	d.scheduler.WaitUntilRunning(t, ctx)
	d.place.WaitUntilRunning(t, ctx)
	d.daprd.WaitUntilRunning(t, ctx)

	// should have the same path separator across OS
	etcdKeysPrefix := "dapr/jobs"

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, d.scheduler.ListAllKeys(t, ctx, etcdKeysPrefix))
	}, time.Second*10, 10*time.Millisecond)

	r := task.NewTaskRegistry()
	require.NoError(t, r.AddOrchestratorN("SingleActivity", func(c *task.OrchestrationContext) (any, error) {
		var input string
		if err := c.GetInput(&input); err != nil {
			return nil, err
		}
		var output string
		err := c.CallActivity("SayHello", task.WithActivityInput(input)).Await(&output)
		return output, err
	}))
	require.NoError(t, r.AddActivityN("SayHello", func(c task.ActivityContext) (any, error) {
		var inp string
		if err := c.GetInput(&inp); err != nil {
			return nil, err
		}

		return fmt.Sprintf("Hello, %s!", inp), nil
	}))

	backendClient := client.NewTaskHubGrpcClient(d.daprd.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, backendClient.StartWorkItemListener(ctx, r))

	resp, err := d.daprd.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "SingleActivity",
		Input:             []byte(`"Dapr"`),
	})
	require.NoError(t, err)

	metadata, err := backendClient.WaitForOrchestrationCompletion(ctx, api.InstanceID(resp.GetInstanceId()))
	require.NoError(t, err)
	assert.True(t, api.OrchestrationMetadataIsComplete(metadata))
	assert.Equal(t, `"Hello, Dapr!"`, metadata.GetOutput().GetValue())

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, d.scheduler.ListAllKeys(t, ctx, etcdKeysPrefix))
	}, time.Second*60, time.Millisecond*10) // account for cleanup time in etcd
	// explicitly not checking the job/counters records since those get garbage collected after 180s
}
