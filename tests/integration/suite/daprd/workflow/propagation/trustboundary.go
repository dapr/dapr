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
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(trustboundary))
}

// trustboundary tests that PropagateOwnHistory creates a trust boundary:
// A -> B -> C, where both hops use OwnHistory (not Lineage). B receives A's
// history but does NOT forward it to C, bc OwnHistory excludes ancestor
// events. C should only see B's own events & never A's.
type trustboundary struct {
	workflow *workflow.Workflow

	totalEvents atomic.Int64
	actACount   atomic.Int64
	actBCount   atomic.Int64
}

func (tb *trustboundary) Setup(t *testing.T) []framework.Option {
	tb.workflow = workflow.New(t,
		workflow.WithMTLS(t),
	)
	return []framework.Option{
		framework.WithProcesses(tb.workflow),
	}
}

func (tb *trustboundary) Run(t *testing.T, ctx context.Context) {
	tb.workflow.WaitUntilRunning(t, ctx)

	reg := tb.workflow.Registry()

	// A calls actA, then creates B with OwnHistory
	reg.AddWorkflowN("wfA", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("actA").Await(nil); err != nil {
			return nil, err
		}
		var result string
		if err := ctx.CallChildWorkflow("wfB",
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&result); err != nil {
			return nil, err
		}
		return result, nil
	})

	// B acts as a trust boundary: propagates its own history to C but does
	// NOT forward A's history because it uses OwnHistory, not Lineage.
	reg.AddWorkflowN("wfB", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("actB").Await(nil); err != nil {
			return nil, err
		}
		var result string
		if err := ctx.CallChildWorkflow("wfC",
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&result); err != nil {
			return nil, err
		}
		return result, nil
	})

	// C reads propagated history, should see B's events only, never A's.
	reg.AddWorkflowN("wfC", func(ctx *task.WorkflowContext) (any, error) {
		ph := ctx.GetPropagatedHistory()
		if ph == nil {
			return statusNoHistory, nil
		}

		tb.totalEvents.Store(int64(len(ph.Events())))
		for _, e := range ph.Events() {
			if ts := e.GetTaskScheduled(); ts != nil {
				switch ts.GetName() {
				case "actA":
					tb.actACount.Add(1)
				case "actB":
					tb.actBCount.Add(1)
				}
			}
		}

		return statusDone, nil
	})

	reg.AddActivityN("actA", func(ctx task.ActivityContext) (any, error) {
		return nil, nil
	})
	reg.AddActivityN("actB", func(ctx task.ActivityContext) (any, error) {
		return nil, nil
	})

	client := tb.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "wfA")
	require.NoError(t, err)

	metadata, err := client.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))

	// Expected 6 events — only B's history. A's events are excluded
	// because B used OwnHistory (not Lineage), creating a trust boundary:
	//
	//   B's own history (A's events excluded):
	//     [0] WorkflowStarted                — B begins
	//     [1] ExecutionStarted                — B's metadata
	//     [2] TaskScheduled("actB")           — B schedules actB
	//     [3] WorkflowStarted                — B replays after actB completes
	//     [4] TaskCompleted                   — actB result
	//     [5] ChildWorkflowInstanceCreated    — B creates C
	assert.Equal(t, int64(6), tb.totalEvents.Load(), "C should receive 6 events: only B's own history (trust boundary)")
	assert.Equal(t, int64(0), tb.actACount.Load(), "C should NOT see actA — OwnHistory blocks ancestor events (trust boundary)")
	assert.Equal(t, int64(1), tb.actBCount.Load(), "C should see actB from B's own history")
}
