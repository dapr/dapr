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
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(retention))
}

// retention verifies the retention purge removes a completed child's
// parent-notify row along with its state.
type retention struct {
	workflow *workflow.Workflow
}

func (r *retention) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: wfpolicy
spec:
  workflow:
    stateRetentionPolicy:
      anyTerminal: "1s"
`)),
	)
	return []framework.Option{framework.WithProcesses(r.workflow)}
}

func (r *retention) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	const childID = "retention-child"
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
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var n int
		if assert.NoError(c, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE key LIKE ?", "%||"+childID+"||%").Scan(&n)) {
			assert.Zero(c, n, "retention must purge the child including its marker")
		}
	}, time.Second*20, time.Millisecond*50)
}
