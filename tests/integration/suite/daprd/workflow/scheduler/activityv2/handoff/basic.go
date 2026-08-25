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

package handoff

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
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
	workflow *workflow.Workflow
	joiners  [2]*daprd.Daprd
}

func (a *basic) Setup(t *testing.T) []framework.Option {
	fp := []daprd.Option{
		daprd.WithFeatureEnabled(t, "WorkflowsFastPath"),
		daprd.WithWorkflowJanitorPeriod(t, time.Second),
	}
	a.workflow = workflow.New(t, workflow.WithDaprdOptions(0, fp...))

	// The joiners trigger the mid-run placement rebalance: deliberately not
	// in WithProcesses, Run starts them at the churn moment.
	for i := range a.joiners {
		a.joiners[i] = daprd.New(t, append([]daprd.Option{
			daprd.WithAppID(a.workflow.Dapr().AppID()),
			daprd.WithResourceFiles(a.workflow.DB().GetComponent(t)),
			daprd.WithPlacementAddresses(a.workflow.Placement().Address()),
			daprd.WithSchedulerAddresses(a.workflow.Scheduler().Address()),
		}, fp...)...)
	}

	return []framework.Option{
		framework.WithProcesses(a.workflow),
	}
}

func (a *basic) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	// Several concurrently blocked activities so at least one actor moves
	// when the joiners rebalance the placement.
	const instances = 4

	var executions atomic.Int64
	started := make(chan struct{}, instances)
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})

	wfFn := func(c *task.WorkflowContext) (any, error) {
		var out string
		if err := c.CallActivity("Slow", task.WithActivityInput("Dapr")).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}
	actFn := func(c task.ActivityContext) (any, error) {
		executions.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
			return "slow-done", nil
		case <-c.Context().Done():
			return nil, c.Context().Err()
		}
	}

	require.NoError(t, a.workflow.Registry().AddWorkflowN("Handoff", wfFn))
	require.NoError(t, a.workflow.Registry().AddActivityN("Slow", actFn))
	client1 := a.workflow.BackendClient(t, ctx)

	ids := make([]string, instances)
	for i := range ids {
		resp, err := a.workflow.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
			WorkflowComponent: "dapr",
			WorkflowName:      "Handoff",
		})
		require.NoError(t, err)
		ids[i] = resp.GetInstanceId()
	}

	for range instances {
		select {
		case <-started:
		case <-time.After(time.Second * 30):
			require.Fail(t, "timed out waiting for every activity body to start executing")
		}
	}
	require.Equal(t, int64(instances), executions.Load(),
		"every activity must be mid-execution before the joiners join")

	startVersion := a.workflow.Placement().PlacementTables(t, ctx).Tables["default"].Version

	// Join the extra daprds so the placement rebalances the in-flight
	// activity actors away from the first host while their bodies are still
	// blocked.
	for i, joiner := range a.joiners {
		joiner.Run(t, ctx)
		t.Cleanup(func() { joiner.Cleanup(t) })
		joiner.WaitUntilRunning(t, ctx)

		registry := task.NewTaskRegistry()
		require.NoError(t, registry.AddWorkflowN("Handoff", wfFn))
		require.NoError(t, registry.AddActivityN("Slow", actFn))
		joinerClient := client.NewTaskHubGrpcClient(joiner.GRPCConn(t, ctx), backend.DefaultLogger())
		require.NoError(t, joinerClient.StartWorkItemListener(ctx, registry))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			table := a.workflow.Placement().PlacementTables(t, ctx).Tables["default"]
			if !assert.NotNil(c, table) {
				return
			}
			//nolint:gosec
			assert.GreaterOrEqual(c, table.Version, startVersion+uint64(i+1),
				"placement table version must advance for each new daprd")
		}, time.Second*15, time.Millisecond*10)
	}

	// Hold the handoff window open across several janitor periods so the
	// re-dispatch (and its second-fire durable-reminder escalation) lands on
	// the new placement owners while the original bodies still execute.
	time.Sleep(time.Second * 4)
	close(release)

	for _, id := range ids {
		metadata, err := client1.WaitForWorkflowCompletion(ctx, api.InstanceID(id))
		require.NoError(t, err)
		assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", metadata.GetRuntimeStatus().String())
	}

	assert.Equal(t, int64(instances), executions.Load(),
		"every activity body must run exactly once even when daprds join the cluster mid-execution")
}
