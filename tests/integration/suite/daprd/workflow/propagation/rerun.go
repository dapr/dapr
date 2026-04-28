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
	suite.Register(new(rerun))
}

// rerun verifies that re-driven activities on a rerun continue to receive
// propagated history when the original scheduling specified a propagation
// scope. Beyond presence checks, the activity inspects ph.GetWorkflows()
// and ph.Events() and asserts on chunk metadata (name/appID/instanceID) &
// event presence (ExecutionStarted, TaskScheduled) on both the original &
// rerun runs.
type rerun struct {
	workflow *procworkflow.Workflow

	activityRuns      atomic.Int32
	activitySawHist   atomic.Bool
	activityEventsLen atomic.Int32
	chunkCount        atomic.Int32
	chunkName         atomic.Value
	chunkAppID        atomic.Value
	chunkInstanceID   atomic.Value
	hasExecStarted    atomic.Bool
	hasTaskScheduled  atomic.Bool
}

func (r *rerun) Setup(t *testing.T) []framework.Option {
	r.workflow = procworkflow.New(t,
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *rerun) reset() {
	r.activityRuns.Store(0)
	r.activitySawHist.Store(false)
	r.activityEventsLen.Store(0)
	r.chunkCount.Store(0)
	r.chunkName.Store("")
	r.chunkAppID.Store("")
	r.chunkInstanceID.Store("")
	r.hasExecStarted.Store(false)
	r.hasTaskScheduled.Store(false)
}

func (r *rerun) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	workflowAppID := r.workflow.DaprN(0).AppID()
	reg := r.workflow.Registry()

	reg.AddActivityN("captureHistory", func(ctx task.ActivityContext) (any, error) {
		r.activityRuns.Add(1)
		ph := ctx.GetPropagatedHistory()
		if ph == nil {
			return "ok", nil
		}
		r.activitySawHist.Store(true)
		r.activityEventsLen.Store(int32(len(ph.Events())))

		workflows := ph.GetWorkflows()
		r.chunkCount.Store(int32(len(workflows)))
		if len(workflows) > 0 {
			r.chunkName.Store(workflows[0].Name)
			r.chunkAppID.Store(workflows[0].AppID)
			r.chunkInstanceID.Store(workflows[0].InstanceID)
		}

		for _, e := range ph.Events() {
			if es := e.GetExecutionStarted(); es != nil && es.GetName() == "parent" {
				r.hasExecStarted.Store(true)
			}
			if ts := e.GetTaskScheduled(); ts != nil && ts.GetName() == "captureHistory" {
				r.hasTaskScheduled.Store(true)
			}
		}
		return "ok", nil
	})

	reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity("captureHistory",
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(nil)
	})

	client := r.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "parent", api.WithInstanceID("rerun-prop"))
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	require.Equal(t, int32(1), r.activityRuns.Load(), "activity should run once on original execution")
	require.True(t, r.activitySawHist.Load(), "activity should receive propagated history on original execution")
	require.Positive(t, r.activityEventsLen.Load(), "original propagated history should be non-empty")
	require.Equal(t, int32(1), r.chunkCount.Load(), "original run should have exactly one chunk (the parent's own events)")
	assert.Equal(t, "parent", r.chunkName.Load(), "original chunk should be tagged with workflow name 'parent'")
	assert.Equal(t, workflowAppID, r.chunkAppID.Load(), "original chunk should be tagged with the test app's appID")
	assert.Equal(t, string(id), r.chunkInstanceID.Load(), "original chunk's instanceID should match the original workflow id")
	assert.True(t, r.hasExecStarted.Load(), "original propagated history should contain ExecutionStarted for 'parent'")
	assert.True(t, r.hasTaskScheduled.Load(), "original propagated history should contain TaskScheduled for 'captureHistory'")

	r.reset()

	// Rerun from event 0: re-drives the captureHistory activity. The
	// re-driven dispatch carries propagated history built from the
	// rerunning workflow's current state, matching the scope persisted on
	// the TaskScheduled event.
	newID, err := client.RerunWorkflowFromEvent(ctx, id, 0)
	require.NoError(t, err)
	assert.NotEqual(t, id, newID)

	_, err = client.WaitForWorkflowCompletion(ctx, newID)
	require.NoError(t, err)

	require.Equal(t, int32(1), r.activityRuns.Load(), "activity should run once on rerun")
	require.True(t, r.activitySawHist.Load(), "activity should receive propagated history on rerun (re-driven dispatch)")
	require.Positive(t, r.activityEventsLen.Load(), "rerun propagated history should be non-empty")
	require.Equal(t, int32(1), r.chunkCount.Load(), "rerun should have exactly one chunk (the rerunning workflow's own events)")
	assert.Equal(t, "parent", r.chunkName.Load(), "rerun chunk should still be tagged with workflow name 'parent'")
	assert.Equal(t, workflowAppID, r.chunkAppID.Load(), "rerun chunk should be tagged with the test app's appID")
	assert.Equal(t, string(newID), r.chunkInstanceID.Load(), "rerun chunk's instanceID should match the rerun workflow id (distinct from original)")
	assert.True(t, r.hasExecStarted.Load(), "rerun propagated history should contain ExecutionStarted for 'parent'")
	assert.True(t, r.hasTaskScheduled.Load(), "rerun propagated history should contain TaskScheduled for 'captureHistory'")
}
