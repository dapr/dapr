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
	"strings"
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
	suite.Register(new(orphanrow))
}

// orphanrow plants a parent-notify row under a root workflow, as a purge on
// a binary that did not know the key leaves behind after the id is reused.
// A parentless instance owes nothing: the row must not read as pending, and
// purge must drop it with the rest of the state.
type orphanrow struct {
	workflow *workflow.Workflow
}

func (o *orphanrow) Setup(t *testing.T) []framework.Option {
	o.workflow = workflow.New(t)
	return []framework.Option{framework.WithProcesses(o.workflow)}
}

func (o *orphanrow) Run(t *testing.T, ctx context.Context) {
	o.workflow.WaitUntilRunning(t, ctx)

	reg := o.workflow.Registry()
	require.NoError(t, reg.AddWorkflowN("root", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.WaitForSingleEvent("go", time.Hour).Await(nil)
	}))
	cl := o.workflow.BackendClient(t, ctx)

	id, err := cl.ScheduleNewWorkflow(ctx, "root", api.WithInstanceID("orphanrow"))
	require.NoError(t, err)
	wf.WaitForRuntimeStatus(t, ctx, cl, id, api.RUNTIME_STATUS_RUNNING)

	db := o.workflow.DB()
	histKey, _ := db.FirstStateValue(t, ctx, string(id), "history")
	rowKey := histKey[:strings.LastIndex(histKey, "||")+2] + "parent-notify"
	db.WriteStateValue(t, ctx, rowKey, []byte{1})

	rows := func(suffix string) int {
		var n int
		require.NoError(t, db.GetConnection(t).QueryRowContext(ctx, "SELECT COUNT(*) FROM "+db.TableName()+" WHERE key LIKE ?", "%||"+string(id)+"||"+suffix).Scan(&n))
		return n
	}
	require.Equal(t, 1, rows("parent-notify"))

	// Complete through a fresh load of the planted row, then purge: a
	// pending notification would refuse the purge, a parentless instance
	// must not.
	require.NoError(t, cl.RaiseEvent(ctx, id, "go"))
	wf.WaitForRuntimeStatus(t, ctx, cl, id, api.RUNTIME_STATUS_COMPLETED)
	require.NoError(t, cl.PurgeWorkflowState(ctx, id))
	assert.Zero(t, rows("%"), "purge removes the orphan row with the rest of the state")
}
