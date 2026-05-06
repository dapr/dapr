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

package propagation

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	procworkflow "github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(activity))
}

// activity tests that PropagateOwnHistory works for activities, not just child
// workflows. The parent calls one activity without propagation, then calls a
// second activity WITH PropagateOwnHistory. The second activity should receive
// the workflow's execution history up to that point.
type activity struct {
	workflow *procworkflow.Workflow

	historyReceived    atomic.Bool
	totalEvents        atomic.Int64
	taskScheduledCount atomic.Int64
	taskCompletedCount atomic.Int64
	wfStartedCount     atomic.Int64
	execStartedCount   atomic.Int64
}

func (a *activity) Setup(t *testing.T) []framework.Option {
	a.workflow = procworkflow.New(t,
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{
		framework.WithProcesses(a.workflow),
	}
}

func (a *activity) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	reg := a.workflow.Registry()

	reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		// Step 1: plain activity, no propagation
		if err := ctx.CallActivity("step1", task.WithActivityInput("a")).Await(nil); err != nil {
			return nil, err
		}

		// Step 2: activity WITH history propagation — should receive the
		// workflow's history including step1's events
		var result string
		if err := ctx.CallActivity("receiver",
			task.WithActivityInput("b"),
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&result); err != nil {
			return nil, err
		}

		return result, nil
	})

	reg.AddActivityN("step1", func(ctx task.ActivityContext) (any, error) {
		return statusDone, nil
	})

	reg.AddActivityN("receiver", func(ctx task.ActivityContext) (any, error) {
		ph := ctx.GetPropagatedHistory()
		if ph == nil {
			return statusNoHistory, nil
		}

		a.historyReceived.Store(true)
		a.totalEvents.Store(int64(len(ph.Events())))
		for _, e := range ph.Events() {
			switch {
			case e.GetTaskScheduled() != nil:
				a.taskScheduledCount.Add(1)
			case e.GetTaskCompleted() != nil:
				a.taskCompletedCount.Add(1)
			case e.GetWorkflowStarted() != nil:
				a.wfStartedCount.Add(1)
			case e.GetExecutionStarted() != nil:
				a.execStartedCount.Add(1)
			}
		}

		return "received", nil
	})

	client := a.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "parent")
	require.NoError(t, err)

	metadata, err := client.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))

	assert.True(t, a.historyReceived.Load(), "activity should have received propagated history")

	// Expected 6 events — the workflow's full history at the time it
	// scheduled the "receiver" activity:
	//     [0] WorkflowStarted                 — orchestrator begins first execution
	//     [1] ExecutionStarted                — workflow metadata (name, input, ID)
	//     [2] TaskScheduled("step1")          — parent schedules step1
	//     [3] WorkflowStarted                 — orchestrator replays for second execution
	//     [4] TaskCompleted                   — step1 result
	//     [5] TaskScheduled("receiver")       — the receiver activity itself being scheduled
	assert.Equal(t, int64(6), a.totalEvents.Load(), "activity should receive exactly 6 events from parent workflow")
	assert.Equal(t, int64(2), a.wfStartedCount.Load(), "2 WorkflowStarted: initial + replay after step1 completes")
	assert.Equal(t, int64(1), a.execStartedCount.Load(), "1 ExecutionStarted: workflow metadata")
	assert.Equal(t, int64(2), a.taskScheduledCount.Load(), "2 TaskScheduled: step1 + receiver (itself)")
	assert.Equal(t, int64(1), a.taskCompletedCount.Load(), "1 TaskCompleted: step1 result")
}
