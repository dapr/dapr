/*
Copyright 2025 The Dapr Authors
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
	suite.Register(new(moved))
}

// moved verifies the reap when placement moves the workflow actor away from
// the host that escalated: the escalation is remembered only on that host, so
// the terminal turn on the new host must sweep the instance's run-activity
// reminders itself. Sixteen instances make a move all but certain with one
// joiner. A leaked reminder would fire once the completed claim record's
// retention lapsed and re-run a body whose workflow already completed.
type moved struct {
	workflow *workflow.Workflow
	joiner   *daprd.Daprd
}

func (m *moved) Setup(t *testing.T) []framework.Option {
	fp := []daprd.Option{
		daprd.WithFeatureEnabled(t, "WorkflowsFastPath"),
		daprd.WithWorkflowJanitorPeriod(t, time.Millisecond*200),
		daprd.WithWorkflowClaimRetention(t, time.Millisecond*200),
	}
	m.workflow = workflow.New(t, workflow.WithDaprdOptions(0, fp...))
	m.joiner = daprd.New(t, append([]daprd.Option{
		daprd.WithAppID(m.workflow.Dapr().AppID()),
		daprd.WithResourceFiles(m.workflow.DB().GetComponent(t)),
		daprd.WithPlacementAddresses(m.workflow.Placement().Address()),
		daprd.WithSchedulerAddresses(m.workflow.Scheduler().Address()),
	}, append(fp, m.workflow.JoinOptions(t)...)...)...)

	return []framework.Option{
		framework.WithProcesses(m.workflow),
	}
}

func (m *moved) Run(t *testing.T, ctx context.Context) {
	m.workflow.WaitUntilRunning(t, ctx)

	const batch = 16
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
			return "done", nil
		case <-c.Context().Done():
			return nil, c.Context().Err()
		}
	}
	require.NoError(t, m.workflow.Registry().AddWorkflowN("EscalationReapMoved", wfFn))
	require.NoError(t, m.workflow.Registry().AddActivityN("Slow", actFn))
	client1 := m.workflow.BackendClient(t, ctx)

	ids := make([]string, 0, batch)
	for range batch {
		resp, err := m.workflow.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
			WorkflowComponent: "dapr",
			WorkflowName:      "EscalationReapMoved",
		})
		require.NoError(t, err)
		ids = append(ids, resp.GetInstanceId())
	}
	require.Eventually(t, func() bool { return executions.Load() >= batch }, time.Second*30, time.Millisecond*10)

	// Two janitor periods with the task unresolved escalate every instance to
	// a durable run-activity reminder while the bodies are still live here.
	escalated := func() float64 {
		return m.workflow.Dapr().Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_activity_count", "status:janitor_redispatch_escalated")
	}
	require.Eventually(t, func() bool { return escalated() >= batch }, time.Second*20, time.Millisecond*10,
		"every unresolved activity must escalate before the move")

	// The joiner takes about half the actors of each type; the bodies keep
	// running on the first host under guarded claims.
	m.joiner.Run(t, ctx)
	t.Cleanup(func() { m.joiner.Cleanup(t) })
	m.joiner.WaitUntilRunning(t, ctx)
	registry := task.NewTaskRegistry()
	require.NoError(t, registry.AddWorkflowN("EscalationReapMoved", wfFn))
	require.NoError(t, registry.AddActivityN("Slow", actFn))
	joinerClient := client.NewTaskHubGrpcClient(m.joiner.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, joinerClient.StartWorkItemListener(ctx, registry))
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, m.joiner.GetMetadata(t, ctx).ActorRuntime.ActiveActors, m.workflow.ActorTypesCount())
	}, time.Second*10, time.Millisecond*10)

	close(release)
	for _, id := range ids {
		metadata, err := client1.WaitForWorkflowCompletion(ctx, api.InstanceID(id))
		require.NoError(t, err)
		assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, metadata.GetRuntimeStatus())
	}

	// Whichever host committed each completion, its reminders must be gone
	// and nothing may run a body after its workflow completed.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Zero(c, m.workflow.Scheduler().JobKeyCount(t, ctx, "run-activity"),
			"no run-activity reminder may outlive its workflow")
	}, time.Second*10, time.Millisecond*10)
	assert.Never(t, func() bool { return executions.Load() > batch }, time.Second*5, time.Millisecond*10,
		"a body ran after its workflow completed: a leaked escalated reminder fired")
}
