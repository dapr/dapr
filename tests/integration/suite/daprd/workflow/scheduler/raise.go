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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
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
	suite.Register(new(raise))
}

type raise struct {
	daprd *daprd.Daprd
}

func (r *raise) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	app := app.New(t)
	place := placement.New(t)
	scheduler := scheduler.New(t)

	r.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithInMemoryActorStateStore("statestore"),
		daprd.WithSchedulerAddresses(scheduler.Address()),
		daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: schedulerreminders
spec:
  features:
  - name: SchedulerReminders
    enabled: true`),
	)

	return []framework.Option{
		framework.WithProcesses(scheduler, place, app, r.daprd),
	}
}

func (r *raise) Run(t *testing.T, ctx context.Context) {
	r.daprd.WaitUntilRunning(t, ctx)

	gclient := r.daprd.GRPCClient(t, ctx)

	var stage atomic.Int64

	reg := task.NewTaskRegistry()
	reg.AddOrchestratorN("foo", func(ctx *task.OrchestrationContext) (any, error) {
		var input int
		require.NoError(t, ctx.GetInput(&input))

		require.NoError(t, ctx.CallActivity("bar", task.WithActivityInput(input)).Await(new([]byte)))
		require.NoError(t, ctx.WaitForSingleEvent("testEvent", time.Second*60).Await(new([]byte)))
		require.NoError(t, ctx.CallActivity("bar", task.WithActivityInput(input)).Await(new([]byte)))

		return "", nil
	})
	require.NoError(t, reg.AddActivityN("bar", func(c task.ActivityContext) (any, error) {
		var input int
		require.NoError(t, c.GetInput(&input))
		stage.Add(int64(input))
		return "", nil
	}))

	backendClient := client.NewTaskHubGrpcClient(r.daprd.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, backendClient.StartWorkItemListener(ctx, reg))

	resp, err := gclient.StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "foo",
		InstanceId:        "my-custom-instance-id",
		Input:             []byte("1"),
	})
	require.NoError(t, err)
	assert.Equal(t, "my-custom-instance-id", resp.GetInstanceId())
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), stage.Load())
	}, time.Second*3, time.Millisecond*10)

	var get *rtv1.GetWorkflowResponse
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		get, err = gclient.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{
			InstanceId:        "my-custom-instance-id",
			WorkflowComponent: "dapr",
		})
		require.NoError(t, err)
		assert.Equal(c, "RUNNING", get.GetRuntimeStatus())
	}, time.Second*5, time.Millisecond*10)

	_, err = gclient.PauseWorkflowBeta1(ctx, &rtv1.PauseWorkflowRequest{
		InstanceId:        "my-custom-instance-id",
		WorkflowComponent: "dapr",
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		get, err = gclient.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{
			InstanceId:        "my-custom-instance-id",
			WorkflowComponent: "dapr",
		})
		require.NoError(t, err)
		assert.Equal(c, "SUSPENDED", get.GetRuntimeStatus())
	}, time.Second*5, time.Millisecond*10)

	_, err = gclient.ResumeWorkflowBeta1(ctx, &rtv1.ResumeWorkflowRequest{
		InstanceId:        "my-custom-instance-id",
		WorkflowComponent: "dapr",
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		get, err = gclient.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{
			InstanceId:        "my-custom-instance-id",
			WorkflowComponent: "dapr",
		})
		require.NoError(t, err)
		assert.Equal(c, "RUNNING", get.GetRuntimeStatus())
	}, time.Second*5, time.Millisecond*10)

	_, err = gclient.RaiseEventWorkflowBeta1(ctx, &rtv1.RaiseEventWorkflowRequest{
		InstanceId:        "my-custom-instance-id",
		WorkflowComponent: "dapr",
		EventName:         "testEvent",
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(2), stage.Load())
	}, time.Second*3, time.Millisecond*10)

	metadata, err := backendClient.WaitForOrchestrationCompletion(ctx, api.InstanceID("my-custom-instance-id"))
	require.NoError(t, err)
	assert.True(t, api.OrchestrationMetadataIsComplete(metadata))

	_, err = gclient.PurgeWorkflowBeta1(ctx, &rtv1.PurgeWorkflowRequest{
		InstanceId:        "my-custom-instance-id",
		WorkflowComponent: "dapr",
	})
	require.NoError(t, err)

	stage.Store(0)

	// Workflow client
	_, err = backendClient.ScheduleNewOrchestration(ctx, "foo", api.WithInstanceID("my-custom-instance-id"), api.WithInput(1))
	require.NoError(t, err)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), stage.Load())
	}, time.Second*3, time.Millisecond*10)

	require.NoError(t, backendClient.RaiseEvent(ctx, "my-custom-instance-id", "testEvent"))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(2), stage.Load())
	}, time.Second*3, time.Millisecond*10)

	_, err = backendClient.WaitForOrchestrationCompletion(ctx, api.InstanceID("my-custom-instance-id"))
	require.NoError(t, err)
}
