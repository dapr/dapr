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

package dedup

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(disseminationcluster))
}

type disseminationcluster struct {
	workflow *workflow.Workflow
	appID    string
	config   string
}

func (d *disseminationcluster) Setup(t *testing.T) []framework.Option {
	d.appID = uuid.New().String()
	d.config = `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: workflowsclustereddeployment
spec:
  features:
  - name: WorkflowsClusteredDeployment
    enabled: true
`

	d.workflow = workflow.New(t,
		workflow.WithPlacementOptions(placement.WithDisseminateTimeout(time.Second*7)),
		workflow.WithDaprdOptions(0,
			daprd.WithAppID(d.appID),
			daprd.WithConfigManifests(t, d.config),
		),
	)
	return []framework.Option{
		framework.WithProcesses(d.workflow),
	}
}

func (d *disseminationcluster) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)

	var activityCalls atomic.Int32
	var startOnce sync.Once
	activityStarted := make(chan struct{})
	releaseActivity := make(chan struct{})

	workflowFn := func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity("slow").Await(nil)
	}
	activityFn := func(ctx task.ActivityContext) (any, error) {
		activityCalls.Add(1)
		startOnce.Do(func() { close(activityStarted) })
		select {
		case <-releaseActivity:
			return nil, nil
		case <-ctx.Context().Done():
			return nil, ctx.Context().Err()
		}
	}

	require.NoError(t, d.workflow.Registry().AddWorkflowN("dedup-disseminationcluster", workflowFn))
	require.NoError(t, d.workflow.Registry().AddActivityN("slow", activityFn))

	cl := d.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "dedup-disseminationcluster")
	require.NoError(t, err)

	select {
	case <-activityStarted:
	case <-time.After(20 * time.Second):
		require.Fail(t, "activity body never started")
	}

	startVersion := d.workflow.Placement().PlacementTables(t, ctx).Tables["default"].Version

	for i := range 2 {
		extra := daprd.New(t,
			daprd.WithAppID(d.appID),
			daprd.WithPlacementAddresses(d.workflow.Placement().Address()),
			daprd.WithScheduler(d.workflow.Scheduler()),
			daprd.WithResourceFiles(d.workflow.DB().GetComponent(t)),
			daprd.WithConfigManifests(t, d.config),
		)
		extra.Run(t, ctx)
		extra.WaitUntilRunning(t, ctx)
		t.Cleanup(func() { extra.Cleanup(t) })

		registry := task.NewTaskRegistry()
		require.NoError(t, registry.AddWorkflowN("dedup-disseminationcluster", workflowFn))
		require.NoError(t, registry.AddActivityN("slow", activityFn))

		extraClient := client.NewTaskHubGrpcClient(extra.GRPCConn(t, ctx), logger.New(t))
		require.NoError(t, extraClient.StartWorkItemListener(ctx, registry))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			table := d.workflow.Placement().PlacementTables(t, ctx).Tables["default"]
			if !assert.NotNil(c, table) {
				return
			}
			//nolint:gosec
			assert.GreaterOrEqual(c, table.Version, startVersion+uint64(i+1),
				"placement table version must advance for each new daprd")
		}, 15*time.Second, 10*time.Millisecond)
	}

	time.Sleep(time.Second * 2)
	close(releaseActivity)

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())

	assert.Equal(t, int32(1), activityCalls.Load(),
		"activity body must run exactly once even when additional daprds join the cluster mid-execution")
}
