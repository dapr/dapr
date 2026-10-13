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

package activityresult

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(purged))
}

type purged struct {
	workflow *workflow.Workflow
}

func (p *purged) Setup(t *testing.T) []framework.Option {
	p.workflow = workflow.New(t,
		workflow.WithFastPath(true),
		workflow.WithDaprdOptions(0, daprd.WithWorkflowJanitorPeriod(t, time.Second)),
	)

	return []framework.Option{
		framework.WithProcesses(p.workflow),
	}
}

func (p *purged) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	var runs atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	reg := dworkflow.NewRegistry()
	require.NoError(t, reg.AddActivityN("gate", func(dworkflow.ActivityContext) (any, error) {
		if runs.Add(1) == 1 {
			close(entered)
			<-release
		}
		return "done", nil
	}))
	require.NoError(t, reg.AddWorkflowN("wf", func(ctx *dworkflow.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity("gate").Await(nil)
	}))

	client := p.workflow.WorkflowClient(t, ctx)
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "wf")
	require.NoError(t, err)

	select {
	case <-entered:
	case <-time.After(time.Second * 10):
		require.Fail(t, "the activity never started")
	}

	require.NoError(t, client.PurgeWorkflowState(ctx, id, dworkflow.WithForcePurge(true)))
	_, err = client.FetchWorkflowMetadata(ctx, id)
	require.Error(t, err, "a purged instance must not be readable")

	close(release)

	executions := func(status string) float64 {
		return p.workflow.Dapr().Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_activity_execution_count",
			"activity_name:gate", "status:"+status)
	}
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.InDelta(c, 1.0, executions("failed"), 0, "the result must be refused as instance not found")
	}, time.Second*10, time.Millisecond*10)

	// Three janitor periods: long enough for any re-dispatch or escalated
	// reminder to have re-run the activity.
	assert.Never(t, func() bool { return runs.Load() > 1 }, time.Second*3, time.Millisecond*50,
		"the activity of a purged workflow must not run again")
	assert.InDelta(t, 1.0, executions("failed"), 0)
	assert.Zero(t, executions("success"))
	assert.Empty(t, p.workflow.Scheduler().ListAllKeys(t, ctx, "dapr/jobs"), "no reminder may be left retrying the result")
}
