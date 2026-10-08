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

package selecttask

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
	"github.com/dapr/kit/concurrency/slice"
)

func init() {
	suite.Register(new(loopremaining))
}

// loopremaining fans out activities with different durations and repeatedly
// Selects over the ones still pending, removing each winner. Every winner is
// a new turn, so a shrinking Select is replayed over real history each time.
type loopremaining struct {
	workflow *workflow.Workflow
	called   slice.Slice[int]
}

func (l *loopremaining) Setup(t *testing.T) []framework.Option {
	l.called = slice.New[int]()
	l.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(l.workflow),
	}
}

func (l *loopremaining) Run(t *testing.T, ctx context.Context) {
	l.workflow.WaitUntilRunning(t, ctx)

	const n = 5
	l.workflow.Registry().AddWorkflowN("loopremaining", func(ctx *task.WorkflowContext) (any, error) {
		pending := make([]task.Task, n)
		inputs := make([]int, n)
		for i := range n {
			pending[i] = ctx.CallActivity("sleep", task.WithActivityInput(i))
			inputs[i] = i
		}

		var order []int
		for len(pending) > 0 {
			winner, err := ctx.Select(pending...)
			if err != nil {
				return nil, err
			}
			var got int
			if err := pending[winner].Await(&got); err != nil {
				return nil, err
			}
			order = append(order, got)
			pending = append(pending[:winner], pending[winner+1:]...)
			inputs = append(inputs[:winner], inputs[winner+1:]...)
		}
		return order, nil
	})
	l.workflow.Registry().AddActivityN("sleep", func(ctx task.ActivityContext) (any, error) {
		var i int
		if err := ctx.GetInput(&i); err != nil {
			return nil, err
		}
		l.called.Append(i)
		time.Sleep(time.Duration(i) * 400 * time.Millisecond)
		return i, nil
	})

	cl := l.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "loopremaining")
	require.NoError(t, err)

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.JSONEq(t, `[0,1,2,3,4]`, meta.GetOutput().GetValue())
	assert.ElementsMatch(t, []int{0, 1, 2, 3, 4}, l.called.Slice())
}
