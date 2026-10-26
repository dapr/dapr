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

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(local))
}

// local pins the reap on the host that escalated: an activity unresolved for
// two janitor periods is escalated to a durable run-activity reminder, and
// the turn that commits its completion reaps that reminder and records it.
type local struct {
	workflow *workflow.Workflow
	release  chan struct{}
}

func (l *local) Setup(t *testing.T) []framework.Option {
	l.release = make(chan struct{})
	l.workflow = workflow.New(t,
		workflow.WithFastPath(true),
		workflow.WithDaprdOptions(0, daprd.WithWorkflowJanitorPeriod(t, time.Millisecond*200)),
	)

	return []framework.Option{
		framework.WithProcesses(l.workflow),
	}
}

func (l *local) Run(t *testing.T, ctx context.Context) {
	l.workflow.WaitUntilRunning(t, ctx)

	var executions atomic.Int64
	l.workflow.Registry().AddWorkflowN("EscalationReapLocal", func(c *task.WorkflowContext) (any, error) {
		return nil, c.CallActivity("Slow").Await(nil)
	})
	l.workflow.Registry().AddActivityN("Slow", func(c task.ActivityContext) (any, error) {
		executions.Add(1)
		select {
		case <-l.release:
			return "done", nil
		case <-c.Context().Done():
			return nil, c.Context().Err()
		}
	})
	cl := l.workflow.BackendClient(t, ctx)

	metric := func(status string) float64 {
		return l.workflow.Dapr().Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_activity_count", "status:"+status)
	}

	id, err := cl.ScheduleNewWorkflow(ctx, "EscalationReapLocal", api.WithStartTime(time.Now()))
	require.NoError(t, err)
	require.Eventually(t, func() bool { return executions.Load() == 1 }, time.Second*10, time.Millisecond*10)

	require.Eventually(t, func() bool { return metric("janitor_redispatch_escalated") >= 1 }, time.Second*10, time.Millisecond*10,
		"the second janitor period must escalate the unresolved activity")
	assert.Zero(t, metric("janitor_escalation_reaped"))

	close(l.release)
	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, metric("janitor_escalation_reaped"), float64(1))
		assert.Zero(c, l.workflow.Scheduler().JobKeyCount(t, ctx, "run-activity"))
	}, time.Second*10, time.Millisecond*10)
	assert.Equal(t, int64(1), executions.Load())
}
