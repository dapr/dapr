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
	suite.Register(new(rerunchild))
}

// rerunchild verifies that re-driven child workflows on a rerun continue to
// receive propagated history when the original CallChildWorkflow specified a
// scope. Beyond presence checks, the child inspects ph.GetWorkflows() and
// ph.Events() and asserts on chunk metadata (parent's name/appID/instanceID)
// and event presence (parent's ExecutionStarted, ChildWorkflowInstanceCreated)
// on both the original and rerun runs.
type rerunchild struct {
	workflow *procworkflow.Workflow

	childSawHist       atomic.Bool
	childEventLen      atomic.Int32
	chunkCount         atomic.Int32
	parentName         atomic.Value
	parentAppID        atomic.Value
	parentInstanceID   atomic.Value
	hasParentExecStart atomic.Bool
	hasChildCreated    atomic.Bool
}

func (r *rerunchild) Setup(t *testing.T) []framework.Option {
	r.workflow = procworkflow.New(t,
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *rerunchild) reset() {
	r.childSawHist.Store(false)
	r.childEventLen.Store(0)
	r.chunkCount.Store(0)
	r.parentName.Store("")
	r.parentAppID.Store("")
	r.parentInstanceID.Store("")
	r.hasParentExecStart.Store(false)
	r.hasChildCreated.Store(false)
}

func (r *rerunchild) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	workflowAppID := r.workflow.DaprN(0).AppID()

	reg := r.workflow.Registry()

	reg.AddWorkflowN("child", func(ctx *task.WorkflowContext) (any, error) {
		ph := ctx.GetPropagatedHistory()
		if ph == nil {
			return "child-done", nil
		}
		r.childSawHist.Store(true)
		r.childEventLen.Store(int32(len(ph.Events())))

		workflows := ph.GetWorkflows()
		r.chunkCount.Store(int32(len(workflows)))
		if len(workflows) > 0 {
			r.parentName.Store(workflows[0].Name)
			r.parentAppID.Store(workflows[0].AppID)
			r.parentInstanceID.Store(workflows[0].InstanceID)
		}

		for _, e := range ph.Events() {
			if es := e.GetExecutionStarted(); es != nil && es.GetName() == "parent" {
				r.hasParentExecStart.Store(true)
			}
			if cw := e.GetChildWorkflowInstanceCreated(); cw != nil && cw.GetName() == "child" {
				r.hasChildCreated.Store(true)
			}
		}
		return "child-done", nil
	})

	reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		return out, ctx.CallChildWorkflow("child",
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&out)
	})

	client := r.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "parent", api.WithInstanceID("rerun-child-prop"))
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	require.True(t, r.childSawHist.Load(), "child should receive propagated history on original execution")
	require.Positive(t, r.childEventLen.Load())
	require.Equal(t, int32(1), r.chunkCount.Load(), "original run should have exactly one chunk (the parent's own events)")
	assert.Equal(t, "parent", r.parentName.Load(), "original chunk should be tagged with parent workflow name")
	assert.Equal(t, workflowAppID, r.parentAppID.Load(), "original chunk should be tagged with the test app's appID")
	assert.Equal(t, string(id), r.parentInstanceID.Load(), "original chunk's instanceID should match the original parent id")
	assert.True(t, r.hasParentExecStart.Load(), "original propagated history should contain ExecutionStarted for 'parent'")
	assert.True(t, r.hasChildCreated.Load(), "original propagated history should contain ChildWorkflowInstanceCreated for 'child'")

	r.reset()

	// Rerun from event 0: re-drives the child workflow. CreateWorkflowInstanceRequest
	// carries a propagated chunk reflecting the rerunning parent's state.
	newID, err := client.RerunWorkflowFromEvent(ctx, id, 0)
	require.NoError(t, err)
	assert.NotEqual(t, id, newID)

	_, err = client.WaitForWorkflowCompletion(ctx, newID)
	require.NoError(t, err)

	require.True(t, r.childSawHist.Load(), "child should receive propagated history on rerun (re-driven dispatch)")
	require.Positive(t, r.childEventLen.Load(), "rerun propagated history should be non-empty")
	require.Equal(t, int32(1), r.chunkCount.Load(), "rerun should have exactly one chunk (the rerunning parent's events)")
	assert.Equal(t, "parent", r.parentName.Load(), "rerun chunk should still be tagged with parent workflow name")
	assert.Equal(t, workflowAppID, r.parentAppID.Load(), "rerun chunk should be tagged with the test app's appID")
	assert.Equal(t, string(newID), r.parentInstanceID.Load(), "rerun chunk's instanceID should match the rerun parent id (distinct from original)")
	assert.True(t, r.hasParentExecStart.Load(), "rerun propagated history should contain ExecutionStarted for 'parent'")
	assert.True(t, r.hasChildCreated.Load(), "rerun propagated history should contain ChildWorkflowInstanceCreated for 'child'")
}
