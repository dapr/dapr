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
	suite.Register(new(driveretry))
}

// driveretry: a placement drain force-cancels the local drive of an activity
// whose body is still executing. The drive must not retry that invocation: the
// retry routes to the new placement owner, which re-runs the body once the
// claim record's retention (compressed to nothing here) has lapsed.
type driveretry struct {
	workflow *workflow.Workflow
	joiners  [2]*daprd.Daprd
	churners [3]*daprd.Daprd
}

func (d *driveretry) Setup(t *testing.T) []framework.Option {
	fp := []daprd.Option{
		daprd.WithFeatureEnabled(t, "WorkflowsFastPath"),
		// The janitor stays out: with the retention compressed a stale
		// re-dispatch would run the body too.
		daprd.WithWorkflowJanitorPeriod(t, time.Second*30),
		daprd.WithWorkflowClaimRetention(t, time.Millisecond),
	}
	d.workflow = workflow.New(t, workflow.WithDaprdOptions(0, fp...))

	newDaprd := func() *daprd.Daprd {
		return daprd.New(t, append([]daprd.Option{
			daprd.WithAppID(d.workflow.Dapr().AppID()),
			daprd.WithResourceFiles(d.workflow.DB().GetComponent(t)),
			daprd.WithPlacementAddresses(d.workflow.Placement().Address()),
			daprd.WithSchedulerAddresses(d.workflow.Scheduler().Address()),
		}, append(fp, d.workflow.JoinOptions(t)...)...)...)
	}
	for i := range d.joiners {
		d.joiners[i] = newDaprd()
	}
	for i := range d.churners {
		d.churners[i] = newDaprd()
	}

	return []framework.Option{
		framework.WithProcesses(d.workflow),
	}
}

func (d *driveretry) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)

	const batch = 6

	var executions atomic.Int64
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
		case <-release:
			return "slow-done", nil
		case <-c.Context().Done():
			return nil, c.Context().Err()
		}
	}

	require.NoError(t, d.workflow.Registry().AddWorkflowN("DriveRetry", wfFn))
	require.NoError(t, d.workflow.Registry().AddActivityN("Slow", actFn))
	client1 := d.workflow.BackendClient(t, ctx)

	var ids []string
	start := func(n int) {
		t.Helper()
		for range n {
			resp, err := d.workflow.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
				WorkflowComponent: "dapr",
				WorkflowName:      "DriveRetry",
			})
			require.NoError(t, err)
			ids = append(ids, resp.GetInstanceId())
		}
		require.Eventually(t, func() bool {
			return executions.Load() >= int64(len(ids))
		}, time.Second*30, time.Millisecond*10)
	}
	start(batch)

	running := []*daprd.Daprd{d.workflow.Dapr()}
	version := d.workflow.Placement().PlacementTables(t, ctx).Tables["default"].Version
	join := func(j *daprd.Daprd) {
		t.Helper()
		j.Run(t, ctx)
		t.Cleanup(func() { j.Cleanup(t) })
		j.WaitUntilRunning(t, ctx)
		running = append(running, j)

		registry := task.NewTaskRegistry()
		require.NoError(t, registry.AddWorkflowN("DriveRetry", wfFn))
		require.NoError(t, registry.AddActivityN("Slow", actFn))
		joinerClient := client.NewTaskHubGrpcClient(j.GRPCConn(t, ctx), backend.DefaultLogger())
		require.NoError(t, joinerClient.StartWorkItemListener(ctx, registry))

		version++
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			table := d.workflow.Placement().PlacementTables(t, ctx).Tables["default"]
			if !assert.NotNil(c, table) {
				return
			}
			assert.GreaterOrEqual(c, table.Version, version)
		}, time.Second*15, time.Millisecond*10)
	}

	for _, joiner := range d.joiners {
		join(joiner)
	}

	// A record proves the churn moved an in-flight actor; its still-blocked
	// body held the drain to its timeout, so that drive was force-cancelled.
	extends := make([]func(), 0, len(d.churners))
	for _, churner := range d.churners {
		extends = append(extends, func() {
			start(batch)
			join(churner)
		})
	}
	wf.EnsureClaimRecords(t, ctx, d.workflow.DB(), extends)

	close(release)

	for _, id := range ids {
		metadata, err := client1.WaitForWorkflowCompletion(ctx, api.InstanceID(id))
		require.NoError(t, err)
		assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", metadata.GetRuntimeStatus().String())
	}

	// A retried drive lands within its backoff cap (2s) of the cancel.
	assert.Never(t, func() bool {
		return executions.Load() > int64(len(ids))
	}, time.Second*3, time.Millisecond*10, "every activity body must run exactly once")

	// The force-cancelled drive skipped its escalation instead of retrying.
	var skipped float64
	for _, dp := range running {
		skipped += dp.Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_activity_count", "status:escalate_skipped_shutdown")
	}
	assert.GreaterOrEqual(t, skipped, float64(1))
}
