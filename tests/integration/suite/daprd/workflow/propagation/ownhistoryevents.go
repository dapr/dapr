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
	suite.Register(new(ownhistoryevents))
}

// ownhistoryevents verifies that PropagateOwnHistory delivers every event
// type the parent produced (WorkflowStarted, ExecutionStarted, TaskScheduled,
// TaskCompleted, ChildWorkflowInstanceCreated), with the expected count per
// type for a 2-activity + 1-child parent workflow.
type ownhistoryevents struct {
	workflow *procworkflow.Workflow

	totalEvents        atomic.Int32
	taskScheduledCount atomic.Int32
	taskCompletedCount atomic.Int32
	cwfCreatedCount    atomic.Int32
	wfStartedCount     atomic.Int32
	execStartedCount   atomic.Int32
}

func (s *ownhistoryevents) Setup(t *testing.T) []framework.Option {
	s.workflow = procworkflow.New(t,
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *ownhistoryevents) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	reg := s.workflow.Registry()

	reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("step1", task.WithActivityInput("a")).Await(nil); err != nil {
			return nil, err
		}
		if err := ctx.CallActivity("step2", task.WithActivityInput("b")).Await(nil); err != nil {
			return nil, err
		}

		var childResult string
		if err := ctx.CallChildWorkflow("child",
			task.WithChildWorkflowInput("test"),
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
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

		s.totalEvents.Store(int32(len(ph.Events())))
		for _, e := range ph.Events() {
			switch {
			case e.GetTaskScheduled() != nil:
				s.taskScheduledCount.Add(1)
			case e.GetTaskCompleted() != nil:
				s.taskCompletedCount.Add(1)
			case e.GetChildWorkflowInstanceCreated() != nil:
				s.cwfCreatedCount.Add(1)
			case e.GetWorkflowStarted() != nil:
				s.wfStartedCount.Add(1)
			case e.GetExecutionStarted() != nil:
				s.execStartedCount.Add(1)
			}
		}

		return "counted", nil
	})

	reg.AddActivityN("step1", func(ctx task.ActivityContext) (any, error) {
		return "done", nil
	})
	reg.AddActivityN("step2", func(ctx task.ActivityContext) (any, error) {
		return "done", nil
	})

	client := s.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "parent")
	require.NoError(t, err)

	metadata, err := client.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	assert.Equal(t, `"counted"`, metadata.GetOutput().GetValue())

	// Expected 9 events — parent's full history across 3 execution batches:
	//
	//   Batch 1 (workflow starts, schedules step1):
	//     [0] WorkflowStarted                — orchestrator begins first execution
	//     [1] ExecutionStarted                — workflow metadata (name, input, ID)
	//     [2] TaskScheduled("step1")          — parent schedules step1
	//
	//   Batch 2 (step1 completes, schedules step2):
	//     [3] WorkflowStarted                — orchestrator replays for second execution
	//     [4] TaskCompleted                   — step1 result
	//     [5] TaskScheduled("step2")          — parent schedules step2
	//
	//   Batch 3 (step2 completes, creates child):
	//     [6] WorkflowStarted                — orchestrator replays for third execution
	//     [7] TaskCompleted                   — step2 result
	//     [8] ChildWorkflowInstanceCreated    — parent creates this child workflow
	assert.Equal(t, int32(9), s.totalEvents.Load(), "child should receive exactly 9 events from parent")
	assert.Equal(t, int32(3), s.wfStartedCount.Load(), "3 WorkflowStarted: initial + replay after each activity completes")
	assert.Equal(t, int32(1), s.execStartedCount.Load(), "1 ExecutionStarted: parent's workflow metadata")
	assert.Equal(t, int32(2), s.taskScheduledCount.Load(), "2 TaskScheduled: step1 and step2")
	assert.Equal(t, int32(2), s.taskCompletedCount.Load(), "2 TaskCompleted: step1 and step2 results")
	assert.Equal(t, int32(1), s.cwfCreatedCount.Load(), "1 ChildWorkflowInstanceCreated: this child workflow")
}
