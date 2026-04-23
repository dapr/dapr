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
	suite.Register(new(activitytrustboundary))
}

// activitytrustboundary verifies that PropagateOwnHistory creates a trust
// boundary for activities, not just child workflows.
// wfA calls wfB with lineage, so B receives A's history. B then
// calls an activity "receiver" with OwnHistory. The activity must see only
// B's own events — A's ancestral events must NOT leak through, even though
// B has them
type activitytrustboundary struct {
	workflow *procworkflow.Workflow

	historyReceived atomic.Bool
	totalEvents     atomic.Int32
	actACount       atomic.Int32
	actBCount       atomic.Int32
	appIDCount      atomic.Int32
	chunkCount      atomic.Int32
}

func (atb *activitytrustboundary) Setup(t *testing.T) []framework.Option {
	atb.workflow = procworkflow.New(t,
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{
		framework.WithProcesses(atb.workflow),
	}
}

func (atb *activitytrustboundary) Run(t *testing.T, ctx context.Context) {
	atb.workflow.WaitUntilRunning(t, ctx)

	reg := atb.workflow.Registry()

	// wfA: calls actA, then creates wfB with PropagateLineage so B receives A's history
	reg.AddWorkflowN("wfA", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("actA").Await(nil); err != nil {
			return nil, err
		}
		var result string
		if err := ctx.CallChildWorkflow("wfB",
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&result); err != nil {
			return nil, err
		}
		return result, nil
	})

	// wfB: received A's history via lineage. Calls actB, then calls "receiver"
	// activity with OwnHistory - B's choice of OwnHistory must mean the
	// activity receives B's events only, NOT A's (even though B has them)
	reg.AddWorkflowN("wfB", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("actB").Await(nil); err != nil {
			return nil, err
		}
		var result string
		if err := ctx.CallActivity("receiver",
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
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

	// receiver: reads propagated history, should see B's events only, never A's, because B chose OwnHistory
	reg.AddActivityN("receiver", func(ctx task.ActivityContext) (any, error) {
		ph := ctx.GetPropagatedHistory()
		if ph == nil {
			return "no history", nil
		}

		atb.historyReceived.Store(true)
		atb.totalEvents.Store(int32(len(ph.Events())))
		atb.appIDCount.Store(int32(len(ph.GetAppIDs())))
		atb.chunkCount.Store(int32(len(ph.GetWorkflows())))

		for _, e := range ph.Events() {
			if ts := e.GetTaskScheduled(); ts != nil {
				switch ts.GetName() {
				case "actA":
					atb.actACount.Add(1)
				case "actB":
					atb.actBCount.Add(1)
				}
			}
		}

		return "received", nil
	})

	client := atb.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "wfA")
	require.NoError(t, err)

	metadata, err := client.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))

	require.True(t, atb.historyReceived.Load(), "activity should have received propagated history")

	// Trust boundary: the activity sees ONLY B's own events.
	// B's events only (A's events excluded by OwnHistory):
	//   [0] WorkflowStarted                 — B begins
	//   [1] ExecutionStarted                 — B's metadata
	//   [2] TaskScheduled("actB")            — B schedules actB
	//   [3] WorkflowStarted                  — B replays after actB completes
	//   [4] TaskCompleted                    — actB result
	//   [5] TaskScheduled("receiver")        — the receiver activity being scheduled
	assert.Equal(t, int32(6), atb.totalEvents.Load(), "activity should receive 6 events: B's own history only (trust boundary)")
	assert.Equal(t, int32(0), atb.actACount.Load(), "activity should NOT see actA — OwnHistory blocks ancestor events")
	assert.Equal(t, int32(1), atb.actBCount.Load(), "activity should see actB from B's own history")

	// only B's chunk, only B's appID
	assert.Equal(t, int32(1), atb.chunkCount.Load(), "should have 1 chunk (B's only, A's chunk excluded by trust boundary)")
	assert.Equal(t, int32(1), atb.appIDCount.Load(), "should have 1 appID (B's only)")
}
