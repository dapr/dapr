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
	suite.Register(new(activitylineage))
}

// activitylineage tests that an activity can receive the full ancestor chain
// via PropagateLineage. Workflow A calls B with Lineage, then B calls an
// activity with Lineage. The activity should see events from both A and B.
type activitylineage struct {
	workflow *procworkflow.Workflow

	historyReceived atomic.Bool
	totalEvents     atomic.Int32
	actACount       atomic.Int32
	actBCount       atomic.Int32
}

func (al *activitylineage) Setup(t *testing.T) []framework.Option {
	al.workflow = procworkflow.New(t,
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{
		framework.WithProcesses(al.workflow),
	}
}

func (al *activitylineage) Run(t *testing.T, ctx context.Context) {
	al.workflow.WaitUntilRunning(t, ctx)

	reg := al.workflow.Registry()

	// Workflow A: calls actA, then creates wfB with lineage propagation.
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

	// Workflow B: calls actB, then calls "receiver" activity with lineage.
	// Because wf B received A's history via lineage, and B uses lineage on the
	// activity call, the receiver should see A's history + B's own history.
	reg.AddWorkflowN("wfB", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("actB", task.WithActivityInput("fromB")).Await(nil); err != nil {
			return nil, err
		}

		var result string
		if err := ctx.CallActivity("receiver",
			task.WithActivityInput("fromB"),
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&result); err != nil {
			return nil, err
		}
		return result, nil
	})

	reg.AddActivityN("actA", func(ctx task.ActivityContext) (any, error) {
		return "doneA", nil
	})
	reg.AddActivityN("actB", func(ctx task.ActivityContext) (any, error) {
		return "doneB", nil
	})

	// Receiver activity: reads propagated history — should see events from
	// both A (ancestor) and B (immediate parent).
	reg.AddActivityN("receiver", func(ctx task.ActivityContext) (any, error) {
		ph := ctx.GetPropagatedHistory()
		if ph == nil {
			return "no history", nil
		}

		al.historyReceived.Store(true)
		al.totalEvents.Store(int32(len(ph.Events())))
		for _, e := range ph.Events() {
			if ts := e.GetTaskScheduled(); ts != nil {
				switch ts.GetName() {
				case "actA":
					al.actACount.Add(1)
				case "actB":
					al.actBCount.Add(1)
				}
			}
		}

		return "received", nil
	})

	client := al.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "wfA")
	require.NoError(t, err)

	metadata, err := client.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))

	assert.True(t, al.historyReceived.Load(), "activity should have received propagated history with lineage")

	// Expected 12 events — A's history (ancestor, forwarded by B) + B's own:
	//   Events [0-5]: A's history (ancestor, forwarded through B via lineage)
	//     [0] WorkflowStarted                — A begins
	//     [1] ExecutionStarted                — A's metadata
	//     [2] TaskScheduled("actA")           — A schedules actA
	//     [3] WorkflowStarted                — A replays after actA completes
	//     [4] TaskCompleted                   — actA result
	//     [5] ChildWorkflowInstanceCreated    — A creates B
	//   Events [6-11]: B's own history
	//     [6] WorkflowStarted                — B begins
	//     [7] ExecutionStarted                — B's metadata
	//     [8] TaskScheduled("actB")           — B schedules actB
	//     [9] WorkflowStarted                — B replays after actB completes
	//     [10] TaskCompleted                  — actB result
	//     [11] TaskScheduled("receiver")      — the receiver activity being scheduled
	assert.Equal(t, int32(12), al.totalEvents.Load(), "activity should receive 12 events: 6 from A (ancestor) + 6 from B (own)")
	assert.Equal(t, int32(1), al.actACount.Load(), "receiver should see actA from A's history (forwarded via lineage through B)")
	assert.Equal(t, int32(1), al.actBCount.Load(), "receiver should see actB from B's own history")
}
