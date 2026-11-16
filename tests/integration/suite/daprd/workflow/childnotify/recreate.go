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

package childnotify

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(recreate))
}

// recreate verifies reusing a completed child's instance id for a new
// workflow leaves no parent-notify row behind, so the new instance never
// re-sends the old completion and a stray fire on it is inert.
type recreate struct {
	workflow *workflow.Workflow
}

func (r *recreate) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t, workflow.WithSigning(false))
	return []framework.Option{framework.WithProcesses(r.workflow)}
}

func (r *recreate) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	const childID = "recreate-child"
	reg := r.workflow.Registry()
	require.NoError(t, reg.AddWorkflowN("quick", func(*task.WorkflowContext) (any, error) {
		return "one", nil
	}))
	require.NoError(t, reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallChildWorkflow("quick", task.WithChildWorkflowInstanceID(childID)).Await(nil)
	}))
	cl := r.workflow.BackendClient(t, ctx)

	id, err := cl.ScheduleNewWorkflow(ctx, "parent")
	require.NoError(t, err)
	_, err = cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	db := r.workflow.DB().GetConnection(t)
	table := r.workflow.DB().TableName()
	markerRows := func() (int, error) {
		var n int
		qerr := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE key LIKE ?", "%||"+childID+"||parent-notify").Scan(&n)
		return n, qerr
	}
	// The child clears the marker only after the parent acknowledges its
	// notification, and that acknowledgement is what releases the parent's
	// own completion turn. The two saves are concurrent, so observing the
	// parent completed does not order the child's write.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		n, qerr := markerRows()
		if assert.NoError(c, qerr) {
			assert.Zero(c, n, "the acknowledged completion must clear the marker")
		}
	}, time.Second*20, time.Millisecond*10)

	// Reuse the id for a root workflow: the recreate resets the state.
	_, err = cl.ScheduleNewWorkflow(ctx, "quick", api.WithInstanceID(childID))
	require.NoError(t, err)
	_, err = cl.WaitForWorkflowCompletion(ctx, childID)
	require.NoError(t, err)
	n, qerr := markerRows()
	require.NoError(t, qerr)
	assert.Zero(t, n, "a recreated instance without a parent carries no marker")

	r.workflow.StrayFire(t, ctx, 0, childID, false)
	completed, _ := wf.ChildCompletions(t, ctx, cl, api.InstanceID(string(id)), 0)
	assert.Equal(t, 1, completed)
}
