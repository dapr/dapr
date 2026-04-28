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
	suite.Register(new(recreaterefresh))
}

// Regression guard for the delete-on-clear path in state.GetSaveRequest
// When a workflow instance is recreated under the same
// ID after completing w/ propagated history, the prior run's
// propagated-history row must be removed from the state store so a future
// reload of that actor does not resurrect stale incoming history.
// App0 parentWf w/ lineage--> App1 childWf (instanceID = "child-abc")
type recreaterefresh struct {
	workflow *procworkflow.Workflow

	firstRunObservedHistory atomic.Bool
}

func (r *recreaterefresh) Setup(t *testing.T) []framework.Option {
	r.workflow = procworkflow.New(t,
		procworkflow.WithDaprds(2),
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *recreaterefresh) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	app0Reg := r.workflow.Registry()
	app1Reg := r.workflow.RegistryN(1)

	const childID = "child-abc"
	app1AppID := r.workflow.DaprN(1).AppID()

	// App0 parent pins the child instance ID so we can recreate under the
	// same ID after completion.
	app0Reg.AddWorkflowN("parentWf", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		err := ctx.CallChildWorkflow("childWf",
			task.WithChildWorkflowAppID(app1AppID),
			task.WithChildWorkflowInstanceID(childID),
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&out)
		if err != nil {
			return nil, err
		}
		return out, nil
	})

	app1Reg.AddWorkflowN("childWf", func(ctx *task.WorkflowContext) (any, error) {
		if ctx.GetPropagatedHistory() != nil {
			r.firstRunObservedHistory.Store(true)
		}
		return "ok", nil
	})

	client0 := r.workflow.BackendClient(t, ctx)
	client1 := r.workflow.BackendClientN(t, ctx, 1)

	parentID, err := client0.ScheduleNewWorkflow(ctx, "parentWf")
	require.NoError(t, err)
	_, err = client0.WaitForWorkflowCompletion(ctx, parentID, api.WithFetchPayloads(true))
	require.NoError(t, err)
	require.True(t, r.firstRunObservedHistory.Load(),
		"first run (via parent) should have received propagated history")

	require.Equal(t, 1, countPropagatedHistoryRows(t, ctx, r.workflow, childID),
		"expected exactly one propagated-history row for the child after run 1")

	// Schedule the child wf on App1 under the same instance ID w/o propagation
	// Verify we don't see the prior runs row.
	_, err = client1.ScheduleNewWorkflow(ctx, "childWf", api.WithInstanceID(childID))
	require.NoError(t, err)
	_, err = client1.WaitForWorkflowCompletion(ctx, childID)
	require.NoError(t, err)
	assert.Equal(t, 0, countPropagatedHistoryRows(t, ctx, r.workflow, childID),
		"propagated-history row must be deleted after recreate-without-propagation")
}

// countPropagatedHistoryRows from the state store
func countPropagatedHistoryRows(t *testing.T, ctx context.Context, wf *procworkflow.Workflow, instanceID string) int {
	t.Helper()
	db := wf.DB().GetConnection(t)
	tableName := wf.DB().TableName()

	likePattern := `%` + escapeLike(instanceID) + `%propagated-history`
	rows, err := db.QueryContext(ctx,
		"SELECT key FROM "+tableName+
			` WHERE key LIKE ? ESCAPE '\'`, likePattern)
	require.NoError(t, err)
	defer rows.Close()

	var n int
	for rows.Next() {
		var key string
		require.NoError(t, rows.Scan(&key))
		n++
	}
	require.NoError(t, rows.Err())
	return n
}
