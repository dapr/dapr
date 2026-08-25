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
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(cleanup))
}

type cleanup struct {
	workflow *workflow.Workflow
	joiners  [2]*daprd.Daprd
}

func (a *cleanup) Setup(t *testing.T) []framework.Option {
	fp := []daprd.Option{
		daprd.WithFeatureEnabled(t, "WorkflowsFastPath"),
		daprd.WithWorkflowJanitorPeriod(t, time.Second),
		// Compress the Completed-record retention so the delete leg fits
		// the test window.
		daprd.WithWorkflowClaimRetention(t, time.Second*2),
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

func (a *cleanup) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	// Higher than the other handoff tests: the record presence assertion
	// needs at least one actor to move, and each of N actors stays put with
	// probability ~1/3.
	const instances = 6

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

	require.NoError(t, a.workflow.Registry().AddWorkflowN("HandoffCleanup", wfFn))
	require.NoError(t, a.workflow.Registry().AddActivityN("Slow", actFn))
	client1 := a.workflow.BackendClient(t, ctx)

	ids := make([]string, instances)
	for i := range ids {
		resp, err := a.workflow.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
			WorkflowComponent: "dapr",
			WorkflowName:      "HandoffCleanup",
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
	require.Zero(t, wf.CountClaimRecords(t, ctx, a.workflow.DB()),
		"no records may exist before any placement churn")

	startVersion := a.workflow.Placement().PlacementTables(t, ctx).Tables["default"].Version

	for i, joiner := range a.joiners {
		joiner.Run(t, ctx)
		t.Cleanup(func() { joiner.Cleanup(t) })
		joiner.WaitUntilRunning(t, ctx)

		registry := task.NewTaskRegistry()
		require.NoError(t, registry.AddWorkflowN("HandoffCleanup", wfFn))
		require.NoError(t, registry.AddActivityN("Slow", actFn))
		joinerClient := client.NewTaskHubGrpcClient(joiner.GRPCConn(t, ctx), backend.DefaultLogger())
		require.NoError(t, joinerClient.StartWorkItemListener(ctx, registry))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			table := a.workflow.Placement().PlacementTables(t, ctx).Tables["default"]
			if !assert.NotNil(c, table) {
				return
			}
			//nolint:gosec
			assert.GreaterOrEqual(c, table.Version, startVersion+uint64(i+1))
		}, time.Second*15, time.Millisecond*10)
	}

	// The rebalance moved some in-flight actors: their guards must surface as
	// records while the bodies still execute on the old host.
	require.Eventually(t, func() bool {
		return wf.CountClaimRecords(t, ctx, a.workflow.DB()) >= 1
	}, time.Second*15, time.Millisecond*50,
		"placement churn over in-flight activities must write execution-claim records")

	close(release)

	for _, id := range ids {
		metadata, err := client1.WaitForWorkflowCompletion(ctx, api.InstanceID(id))
		require.NoError(t, err)
		assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", metadata.GetRuntimeStatus().String())
	}
	assert.Equal(t, int64(instances), executions.Load())

	// Retention (2s) then delete: no record may leak past completion.
	assert.Eventually(t, func() bool {
		return wf.CountClaimRecords(t, ctx, a.workflow.DB()) == 0
	}, time.Second*30, time.Millisecond*100,
		"every execution-claim record must self-delete after the retention window")
}
