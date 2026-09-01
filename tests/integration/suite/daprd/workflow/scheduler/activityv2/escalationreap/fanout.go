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

package escalationreap

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
	suite.Register(new(fanout))
}

// escalationreap verifies the durable run-activity reminder armed by a janitor
// escalation is reaped once its task resolves. The escalation can only land
// during a handoff window (the dissemination cancels the in-flight execution's
// wait and frees the activity actor lock), and the resolving execution
// predates the escalation, so nothing else deletes the reminder and its fire
// re-runs the body on a cold host.
type fanout struct {
	workflow *workflow.Workflow
	joiner   *daprd.Daprd
}

func (e *fanout) Setup(t *testing.T) []framework.Option {
	fp := []daprd.Option{
		daprd.WithFeatureEnabled(t, "WorkflowsFastPath"),
		daprd.WithWorkflowJanitorPeriod(t, time.Millisecond*200),
	}
	e.workflow = workflow.New(t, workflow.WithDaprdOptions(0, fp...))

	e.joiner = daprd.New(t, append([]daprd.Option{
		daprd.WithAppID(e.workflow.Dapr().AppID()),
		daprd.WithResourceFiles(e.workflow.DB().GetComponent(t)),
		daprd.WithPlacementAddresses(e.workflow.Placement().Address()),
		daprd.WithSchedulerAddresses(e.workflow.Scheduler().Address()),
	}, fp...)...)

	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *fanout) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	const batch = 2
	const fanout = 4

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
		tasks := make([]task.Task, fanout)
		for i := range tasks {
			tasks[i] = c.CallActivity("Slow")
		}
		for _, tk := range tasks {
			if err := tk.Await(nil); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}
	actFn := func(c task.ActivityContext) (any, error) {
		executions.Add(1)
		select {
		case <-release:
			return nil, nil
		case <-c.Context().Done():
			return nil, c.Context().Err()
		}
	}

	require.NoError(t, e.workflow.Registry().AddWorkflowN("EscalationReapFanout", wfFn))
	require.NoError(t, e.workflow.Registry().AddActivityN("Slow", actFn))
	client1 := e.workflow.BackendClient(t, ctx)

	ids := make([]string, 0, batch)
	for range batch {
		resp, err := e.workflow.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
			WorkflowComponent: "dapr",
			WorkflowName:      "EscalationReapFanout",
		})
		require.NoError(t, err)
		ids = append(ids, resp.GetInstanceId())
	}
	require.Eventually(t, func() bool {
		return executions.Load() >= int64(batch*fanout)
	}, time.Second*30, time.Millisecond*10,
		"every activity body must be mid-execution before the churn")

	e.joiner.Run(t, ctx)
	t.Cleanup(func() { e.joiner.Cleanup(t) })
	e.joiner.WaitUntilRunning(t, ctx)

	registry := task.NewTaskRegistry()
	require.NoError(t, registry.AddWorkflowN("EscalationReapFanout", wfFn))
	require.NoError(t, registry.AddActivityN("Slow", actFn))
	joinerClient := client.NewTaskHubGrpcClient(e.joiner.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, joinerClient.StartWorkItemListener(ctx, registry))

	sumBoth := func(status string) float64 {
		return e.workflow.Dapr().Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_activity_count", "status:"+status) +
			e.joiner.Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_activity_count", "status:"+status)
	}
	require.Eventually(t, func() bool {
		return sumBoth("janitor_redispatch_escalated") >= 1
	}, time.Second*30, time.Millisecond*10,
		"the janitor must escalate at least one unresolved activity to its durable reminder")

	close(release)
	for _, id := range ids {
		metadata, err := client1.WaitForWorkflowCompletion(ctx, api.InstanceID(id))
		require.NoError(t, err)
		assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", metadata.GetRuntimeStatus().String())
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, sumBoth("janitor_escalation_reaped"), float64(2),
			"every escalated task of the fan-out must be reaped, not only the first")
		assert.Zero(c, e.workflow.Scheduler().JobKeyCount(t, ctx, "run-activity"),
			"no run-activity reminder may outlive its workflow")
	}, time.Second*30, time.Millisecond*10)

	assert.Equal(t, int64(batch*fanout), executions.Load(),
		"every activity body must run exactly once; a reaped reminder cannot fire")
}
