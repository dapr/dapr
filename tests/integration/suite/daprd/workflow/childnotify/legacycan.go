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
	suite.Register(new(legacycan))
}

// legacycan models a signed child created before the creation input was
// recorded that has already continued as new: its row is removed while it
// waits in its second generation. When it continues as new again, nothing may
// be inferred from a start event that carries the continued input, so no
// creation input is written.
type legacycan struct {
	workflow *workflow.Workflow
}

func (l *legacycan) Setup(t *testing.T) []framework.Option {
	l.workflow = workflow.New(t, workflow.WithDaprds(2), workflow.WithMTLS(t))
	return []framework.Option{framework.WithProcesses(l.workflow)}
}

func (l *legacycan) Run(t *testing.T, ctx context.Context) {
	l.workflow.WaitUntilRunning(t, ctx)

	const childID = "legacycan-child"
	childApp := l.workflow.DaprN(1).AppID()
	require.NoError(t, l.workflow.RegistryN(1).AddWorkflowN("child", func(ctx *task.WorkflowContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		switch input {
		case "first":
			ctx.ContinueAsNew("second")
			return nil, nil
		case "second":
			if err := ctx.WaitForSingleEvent("go", time.Hour).Await(nil); err != nil {
				return nil, err
			}
			ctx.ContinueAsNew("third")
			return nil, nil
		}
		if err := ctx.WaitForSingleEvent("go", time.Hour).Await(nil); err != nil {
			return nil, err
		}
		return input, nil
	}))
	require.NoError(t, l.workflow.Registry().AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallChildWorkflow("child",
			task.WithChildWorkflowInput("first"),
			task.WithChildWorkflowInstanceID(childID),
			task.WithChildWorkflowAppID(childApp),
		).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	cl := l.workflow.BackendClient(t, ctx)
	child := l.workflow.BackendClientN(t, ctx, 1)

	_, err := cl.ScheduleNewWorkflow(ctx, "parent")
	require.NoError(t, err)
	waitingWith := func(input string) {
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			meta, merr := child.FetchWorkflowMetadata(ctx, childID, api.WithFetchPayloads(true))
			if assert.NoError(c, merr) {
				assert.Equal(c, api.RUNTIME_STATUS_RUNNING, meta.GetRuntimeStatus())
				assert.JSONEq(c, `"`+input+`"`, meta.GetInput().GetValue())
			}
		}, time.Second*20, time.Millisecond*10)
	}
	waitingWith("second")

	db := l.workflow.DB()
	histKey, _ := db.FirstStateValue(t, ctx, childID, "history")
	rowKey := histKey[:strings.LastIndex(histKey, "||")+2] + "creation-input"
	rows := func() int {
		var n int
		require.NoError(t, db.GetConnection(t).QueryRowContext(ctx, "SELECT COUNT(*) FROM "+db.TableName()+" WHERE key = ?", rowKey).Scan(&n))
		return n
	}
	require.Equal(t, 1, rows(), "a signed child records its creation input")
	_, err = db.GetConnection(t).ExecContext(ctx, "DELETE FROM "+db.TableName()+" WHERE key = ?", rowKey)
	require.NoError(t, err)

	// A cold load sees no row, as for a child created before the row existed.
	l.workflow.DaprN(1).RestartGraceful(t, ctx)
	l.workflow.WaitUntilRunning(t, ctx)
	child = l.workflow.BackendClientN(t, ctx, 1)

	require.NoError(t, child.RaiseEvent(ctx, childID, "go"))
	waitingWith("third")
	assert.Zero(t, rows(), "the continued input must not be recorded as the creation input")

	require.NoError(t, child.RaiseEvent(ctx, childID, "go"))
	wf.WaitForRuntimeStatus(t, ctx, child, childID, api.RUNTIME_STATUS_COMPLETED)
	assert.Zero(t, rows())
}
