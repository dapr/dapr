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
	"errors"
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
	suite.Register(new(failed))
}

// failed verifies the reap also settles escalations whose task resolves by
// FAILING: a TaskFailed commit is as final as a completion, and an unreaped
// reminder would re-run the failing body on a cold host.
type failed struct {
	workflow *workflow.Workflow
	// joiners trigger placement rebalances; one joins up front and the rest
	// join one at a time only while no escalation has landed, since a churn
	// round can move no in-flight actor at all.
	joiners [3]*daprd.Daprd
}

func (e *failed) Setup(t *testing.T) []framework.Option {
	fp := []daprd.Option{
		daprd.WithFeatureEnabled(t, "WorkflowsFastPath"),
		daprd.WithWorkflowJanitorPeriod(t, time.Millisecond*200),
	}
	e.workflow = workflow.New(t, workflow.WithDaprdOptions(0, fp...))

	for i := range e.joiners {
		e.joiners[i] = daprd.New(t, append([]daprd.Option{
			daprd.WithAppID(e.workflow.Dapr().AppID()),
			daprd.WithResourceFiles(e.workflow.DB().GetComponent(t)),
			daprd.WithPlacementAddresses(e.workflow.Placement().Address()),
			daprd.WithSchedulerAddresses(e.workflow.Scheduler().Address()),
		}, fp...)...)
	}

	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *failed) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	const batch = 6
	const bodiesPerWorkflow = 1

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
		return nil, c.CallActivity("Slow").Await(nil)
	}
	actFn := func(c task.ActivityContext) (any, error) {
		executions.Add(1)
		select {
		case <-release:
			return nil, errors.New("deliberate activity failure")
		case <-c.Context().Done():
			return nil, c.Context().Err()
		}
	}

	require.NoError(t, e.workflow.Registry().AddWorkflowN("EscalationReapFailed", wfFn))
	require.NoError(t, e.workflow.Registry().AddActivityN("Slow", actFn))
	client1 := e.workflow.BackendClient(t, ctx)

	var ids []string
	start := func() {
		t.Helper()
		for range batch {
			resp, err := e.workflow.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
				WorkflowComponent: "dapr",
				WorkflowName:      "EscalationReapFailed",
			})
			require.NoError(t, err)
			ids = append(ids, resp.GetInstanceId())
		}
		require.Eventually(t, func() bool {
			return executions.Load() >= int64(len(ids)*bodiesPerWorkflow)
		}, time.Second*30, time.Millisecond*10,
			"every activity body must be mid-execution before the churn")
	}
	start()

	var joined []*daprd.Daprd
	join := func(d *daprd.Daprd) {
		t.Helper()
		d.Run(t, ctx)
		t.Cleanup(func() { d.Cleanup(t) })
		d.WaitUntilRunning(t, ctx)

		registry := task.NewTaskRegistry()
		require.NoError(t, registry.AddWorkflowN("EscalationReapFailed", wfFn))
		require.NoError(t, registry.AddActivityN("Slow", actFn))
		joinerClient := client.NewTaskHubGrpcClient(d.GRPCConn(t, ctx), backend.DefaultLogger())
		require.NoError(t, joinerClient.StartWorkItemListener(ctx, registry))
		joined = append(joined, d)
	}
	join(e.joiners[0])

	sumBoth := func(status string) float64 {
		sum := e.workflow.Dapr().Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_activity_count", "status:"+status)
		for _, d := range joined {
			sum += d.Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_activity_count", "status:"+status)
		}
		return sum
	}
	// Whether a churn round moves any in-flight actor is probabilistic; when
	// none moved, block another batch and churn again with the next joiner.
	escalated := func() bool { return sumBoth("janitor_redispatch_escalated") >= 1 }
	for _, churner := range e.joiners[1:] {
		deadline := time.Now().Add(time.Second * 10)
		for !escalated() && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond * 50)
		}
		if escalated() {
			break
		}
		start()
		join(churner)
	}
	require.Eventually(t, escalated, time.Second*10, time.Millisecond*50,
		"the janitor must escalate at least one unresolved activity to its durable reminder")

	close(release)
	for _, id := range ids {
		metadata, err := client1.WaitForWorkflowCompletion(ctx, api.InstanceID(id))
		require.NoError(t, err)
		assert.Equal(t, "ORCHESTRATION_STATUS_FAILED", metadata.GetRuntimeStatus().String())
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, sumBoth("janitor_escalation_reaped"), float64(1))
		assert.Zero(c, e.workflow.Scheduler().JobKeyCount(t, ctx, "run-activity"),
			"no run-activity reminder may outlive its workflow")
	}, time.Second*30, time.Millisecond*50)

	// A failing body is deliberately at-least-once at handoff: an execution
	// error deletes the claim record so the new owner re-executes. The reap
	// guarantee is that nothing re-runs a body after its TaskFailed
	// committed: the count must be settled once the reminders are gone.
	settled := executions.Load()
	assert.GreaterOrEqual(t, settled, int64(len(ids)))
	assert.LessOrEqual(t, settled, int64(len(ids)*2),
		"a failing body may re-execute once per handoff, never more")
	time.Sleep(time.Second * 2)
	assert.Equal(t, settled, executions.Load(),
		"no body may run after its workflow failed; a reaped reminder cannot fire")
}
