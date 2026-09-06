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
	"strconv"
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
	suite.Register(new(parentterminated))
}

// parentterminated verifies a recursive terminate settles: the children's
// completions reach an already terminated parent, which must ack them without
// re-running its terminal turn, otherwise the cascade terminate and the
// children's re-sends feed each other forever.
type parentterminated struct {
	workflow *workflow.Workflow
}

func (p *parentterminated) Setup(t *testing.T) []framework.Option {
	p.workflow = workflow.New(t)
	return []framework.Option{framework.WithProcesses(p.workflow)}
}

func (p *parentterminated) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	const children = 3
	childID := func(i int) string { return "parentterminated-child-" + strconv.Itoa(i) }

	reg := p.workflow.Registry()
	require.NoError(t, reg.AddWorkflowN("child", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.WaitForSingleEvent("go", time.Hour).Await(nil)
	}))
	require.NoError(t, reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		tasks := make([]task.Task, children)
		for i := range children {
			tasks[i] = ctx.CallChildWorkflow("child", task.WithChildWorkflowInstanceID(childID(i)))
		}
		for _, tk := range tasks {
			if err := tk.Await(nil); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}))
	cl := p.workflow.BackendClient(t, ctx)

	id, err := cl.ScheduleNewWorkflow(ctx, "parent", api.WithInstanceID("parentterminated"))
	require.NoError(t, err)
	for i := range children {
		wf.WaitForRuntimeStatus(t, ctx, cl, api.InstanceID(childID(i)), api.RUNTIME_STATUS_RUNNING)
	}

	termCtx, cancel := context.WithTimeout(ctx, time.Second*5)
	t.Cleanup(cancel)
	require.NoError(t, cl.TerminateWorkflow(termCtx, id))
	wf.WaitForRuntimeStatus(t, ctx, cl, id, api.RUNTIME_STATUS_TERMINATED)
	for i := range children {
		wf.WaitForRuntimeStatus(t, ctx, cl, api.InstanceID(childID(i)), api.RUNTIME_STATUS_TERMINATED)
	}

	db := p.workflow.DB().GetConnection(t)
	table := p.workflow.DB().TableName()
	metadataETag := func() string {
		var etag string
		require.NoError(t, db.QueryRowContext(ctx, "SELECT etag FROM "+table+" WHERE key LIKE ?", "%||"+string(id)+"||metadata").Scan(&etag))
		return etag
	}
	before := metadataETag()

	// A ping-pong shows itself within a second; give it two before asserting
	// the parent never wrote again and no reminder is still cycling.
	time.Sleep(time.Second * 2)
	assert.Equal(t, before, metadataETag(), "the terminated parent must not re-run its terminal turn on late child completions")
	var inbox int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE key LIKE ?", "%||"+string(id)+"||inbox-%").Scan(&inbox))
	assert.Zero(t, inbox)
	sched := p.workflow.Scheduler()
	assert.Zero(t, sched.JobKeyCount(t, ctx, "cascade-terminate"))
	assert.Zero(t, sched.JobKeyCount(t, ctx, "parent-notify"))
}
