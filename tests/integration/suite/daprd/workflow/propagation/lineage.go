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
	suite.Register(new(lineage))
}

// lineage tests multi-hop propagation: A -> B -> C, where each hop uses
// PropagateLineage. C should see the full ancestor chain events from both
// A & B bc B forwards A's history onward alongside its own
type lineage struct {
	workflow *procworkflow.Workflow

	totalEvents atomic.Int32
	actACount   atomic.Int32
	actBCount   atomic.Int32
}

func (l *lineage) Setup(t *testing.T) []framework.Option {
	l.workflow = procworkflow.New(t,
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{
		framework.WithProcesses(l.workflow),
	}
}

func (l *lineage) Run(t *testing.T, ctx context.Context) {
	l.workflow.WaitUntilRunning(t, ctx)

	reg := l.workflow.Registry()

	// wf A: calls actA, then creates B with lineage propagation.
	reg.AddWorkflowN("wfA", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("actA", task.WithActivityInput("fromA")).Await(nil); err != nil {
			return nil, err
		}

		var result string
		if err := ctx.CallChildWorkflow("wfB",
			task.WithChildWorkflowInput("fromA"),
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&result); err != nil {
			return nil, err
		}
		return result, nil
	})

	// wf B: calls actB, then creates C with lineage propagation.
	// Because B received A's history via lineage, and B also uses lineage
	// when calling C, C will receive A's history + B's own history.
	reg.AddWorkflowN("wfB", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("actB", task.WithActivityInput("fromB")).Await(nil); err != nil {
			return nil, err
		}

		var result string
		if err := ctx.CallChildWorkflow("wfC",
			task.WithChildWorkflowInput("fromB"),
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&result); err != nil {
			return nil, err
		}
		return result, nil
	})

	// Workflow C: reads propagated history & sees the full chain
	reg.AddWorkflowN("wfC", func(ctx *task.WorkflowContext) (any, error) {
		ph := ctx.GetPropagatedHistory()
		if ph == nil {
			return "no history", nil //nolint:goconst
		}

		l.totalEvents.Store(int32(len(ph.Events())))
		for _, e := range ph.Events() {
			if ts := e.GetTaskScheduled(); ts != nil {
				switch ts.GetName() {
				case "actA":
					l.actACount.Add(1)
				case "actB":
					l.actBCount.Add(1)
				}
			}
		}

		return "done", nil
	})

	reg.AddActivityN("actA", func(ctx task.ActivityContext) (any, error) {
		return "doneA", nil
	})
	reg.AddActivityN("actB", func(ctx task.ActivityContext) (any, error) {
		return "doneB", nil
	})

	client := l.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "wfA")
	require.NoError(t, err)

	metadata, err := client.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))

	// Expected 12 events — A's history (forwarded by B via lineage) + B's own:
	//
	//   Events [0-5]: A's history (ancestor, forwarded through B)
	//     [0] WorkflowStarted                — A begins
	//     [1] ExecutionStarted                — A's metadata
	//     [2] TaskScheduled("actA")           — A schedules actA
	//     [3] WorkflowStarted                — A replays after actA completes
	//     [4] TaskCompleted                   — actA result
	//     [5] ChildWorkflowInstanceCreated    — A creates B
	//
	//   Events [6-11]: B's own history
	//     [6] WorkflowStarted                — B begins
	//     [7] ExecutionStarted                — B's metadata
	//     [8] TaskScheduled("actB")           — B schedules actB
	//     [9] WorkflowStarted                — B replays after actB completes
	//     [10] TaskCompleted                  — actB result
	//     [11] ChildWorkflowInstanceCreated   — B creates C
	assert.Equal(t, int32(12), l.totalEvents.Load(), "C should receive 12 events: 6 from A (ancestor) + 6 from B (own)")
	assert.Equal(t, int32(1), l.actACount.Load(), "C should see actA from A's history (forwarded via lineage through B)")
	assert.Equal(t, int32(1), l.actBCount.Load(), "C should see actB from B's own history")
}
