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

package workflow

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(parallel))
}

type parallel struct {
	legacy    *daprd.Daprd
	scheduler *daprd.Daprd
}

func (p *parallel) Setup(t *testing.T) []framework.Option {
	app := app.New(t)
	place := placement.New(t)
	scheduler := scheduler.New(t)

	opts := []daprd.Option{
		daprd.WithAppPort(app.Port()),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithInMemoryActorStateStore("statestore"),
		daprd.WithSchedulerAddresses(scheduler.Address()),
	}

	p.legacy = daprd.New(t, opts...)
	p.scheduler = daprd.New(t, opts...)

	return []framework.Option{
		framework.WithProcesses(scheduler, place, app, p.legacy, p.scheduler),
	}
}

func (p *parallel) Run(t *testing.T, ctx context.Context) {
	p.legacy.WaitUntilRunning(t, ctx)
	p.scheduler.WaitUntilRunning(t, ctx)

	for _, daprd := range []*daprd.Daprd{p.legacy, p.scheduler} {
		releaseCh := make(chan struct{})
		var activityWaiting atomic.Int64

		r := task.NewTaskRegistry()
		require.NoError(t, r.AddOrchestratorN("foo", func(c *task.OrchestrationContext) (any, error) {
			ts := make([]task.Task, 10)
			for i := range ts {
				ts[i] = c.CallActivity("bar", task.WithActivityInput(strconv.Itoa(i)))
			}
			for _, task := range ts {
				require.NoError(t, task.Await(nil))
			}
			return nil, nil
		}))
		require.NoError(t, r.AddActivityN("bar", func(c task.ActivityContext) (any, error) {
			activityWaiting.Add(1)
			select {
			case <-releaseCh:
			case <-ctx.Done():
				require.Fail(t, "timeout waiting for activities to release")
			}
			return nil, nil
		}))

		backendClient := client.NewTaskHubGrpcClient(daprd.GRPCConn(t, ctx), backend.DefaultLogger())
		require.NoError(t, backendClient.StartWorkItemListener(ctx, r))
		resp, err := daprd.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
			WorkflowComponent: "dapr",
			WorkflowName:      "foo",
		})
		require.NoError(t, err)

		assert.Eventually(t, func() bool {
			return activityWaiting.Load() == 10
		}, 10*time.Second, 10*time.Millisecond)
		close(releaseCh)

		metadata, err := backendClient.WaitForOrchestrationCompletion(ctx, api.InstanceID(resp.GetInstanceId()))
		require.NoError(t, err)
		assert.True(t, api.OrchestrationMetadataIsComplete(metadata))
	}
}
