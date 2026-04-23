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
	suite.Register(new(childworkflow))
}

// childworkflow tests history propagation to a child wf using
// PropagateLineage. The parent calls one activity, then creates the child.
// The child should receive the parent's full execution history up to that point.
type childworkflow struct {
	workflow *procworkflow.Workflow

	totalEvents        atomic.Int32
	taskScheduledCount atomic.Int32
	taskCompletedCount atomic.Int32
	cwfCreatedCount    atomic.Int32
	wfStartedCount     atomic.Int32
	execStartedCount   atomic.Int32
	historyReceived    atomic.Bool
}

func (c *childworkflow) Setup(t *testing.T) []framework.Option {
	c.workflow = procworkflow.New(t,
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *childworkflow) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	reg := c.workflow.Registry()

	reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("greet", task.WithActivityInput("world")).Await(nil); err != nil {
			return nil, err
		}

		var childResult string
		if err := ctx.CallChildWorkflow("child",
			task.WithChildWorkflowInput("from-parent"),
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&childResult); err != nil {
			return nil, err
		}

		return childResult, nil
	})

	reg.AddWorkflowN("child", func(ctx *task.WorkflowContext) (any, error) {
		ph := ctx.GetPropagatedHistory()
		if ph == nil {
			return "no history", nil
		}

		c.historyReceived.Store(true)
		c.totalEvents.Store(int32(len(ph.Events())))
		for _, e := range ph.Events() {
			switch {
			case e.GetTaskScheduled() != nil:
				c.taskScheduledCount.Add(1)
			case e.GetTaskCompleted() != nil:
				c.taskCompletedCount.Add(1)
			case e.GetChildWorkflowInstanceCreated() != nil:
				c.cwfCreatedCount.Add(1)
			case e.GetWorkflowStarted() != nil:
				c.wfStartedCount.Add(1)
			case e.GetExecutionStarted() != nil:
				c.execStartedCount.Add(1)
			}
		}

		return "done", nil
	})

	reg.AddActivityN("greet", func(ctx task.ActivityContext) (any, error) {
		return "Hello!", nil
	})

	client := c.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "parent", api.WithInput("test"))
	require.NoError(t, err)

	metadata, err := client.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))

	assert.True(t, c.historyReceived.Load(), "child workflow should have received propagated history")

	// Expected 6 events: the parent's full history at the time it created the child workflow:
	//   Batch 1 (wf starts, schedules greet):
	//     [0] WorkflowStarted                — orchestrator begins first execution
	//     [1] ExecutionStarted                — workflow metadata (name, input, ID)
	//     [2] TaskScheduled("greet")          — parent schedules the greet activity
	//
	//   Batch 2 (greet completes, parent creates child):
	//     [3] WorkflowStarted                — orchestrator replays for second execution
	//     [4] TaskCompleted                   — greet activity result
	//     [5] ChildWorkflowInstanceCreated    — parent creates this child wf
	assert.Equal(t, int32(6), c.totalEvents.Load(), "child should receive exactly 6 events from parent")
	assert.Equal(t, int32(2), c.wfStartedCount.Load(), "2 WorkflowStarted: initial + replay after greet completes")
	assert.Equal(t, int32(1), c.execStartedCount.Load(), "1 ExecutionStarted: parent's workflow metadata")
	assert.Equal(t, int32(1), c.taskScheduledCount.Load(), "1 TaskScheduled: the greet activity")
	assert.Equal(t, int32(1), c.taskCompletedCount.Load(), "1 TaskCompleted: greet activity result")
	assert.Equal(t, int32(1), c.cwfCreatedCount.Load(), "1 ChildWorkflowInstanceCreated: this child workflow")
}
