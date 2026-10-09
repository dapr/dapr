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
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
	"github.com/dapr/kit/concurrency/slice"
)

func init() {
	suite.Register(new(loopremaining))
}

// loopremaining fans out blocked activities and repeatedly Selects over the
// ones still pending, removing each winner. The test releases the activities
// one at a time, so every winner is a new turn and a shrinking Select is
// replayed over real history each time.
type loopremaining struct {
	workflow *workflow.Workflow
	called   slice.Slice[int]
	release  [loopremainingN]chan struct{}
	once     [loopremainingN]sync.Once
}

const loopremainingN = 5

func (l *loopremaining) Setup(t *testing.T) []framework.Option {
	l.called = slice.New[int]()
	for i := range l.release {
		l.release[i] = make(chan struct{})
	}
	l.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(l.workflow),
	}
}

func (l *loopremaining) Run(t *testing.T, ctx context.Context) {
	l.workflow.WaitUntilRunning(t, ctx)

	l.workflow.Registry().AddWorkflowN("loopremaining", func(ctx *task.WorkflowContext) (any, error) {
		pending := make([]task.Task, loopremainingN)
		for i := range pending {
			pending[i] = ctx.CallActivity("blocked", task.WithActivityInput(i))
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
		}
		return order, nil
	})
	l.workflow.Registry().AddActivityN("blocked", func(ctx task.ActivityContext) (any, error) {
		var i int
		if err := ctx.GetInput(&i); err != nil {
			return nil, err
		}
		l.called.Append(i)
		<-l.release[i]
		return i, nil
	})

	release := func(i int) { l.once[i].Do(func() { close(l.release[i]) }) }
	t.Cleanup(func() {
		for i := range l.release {
			release(i)
		}
	})

	cl := l.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "loopremaining")
	require.NoError(t, err)

	// Release the activities in index order, waiting for each completion to
	// be persisted before releasing the next, so the completion order is
	// fixed. Activity i was scheduled as task i.
	for i := range loopremainingN {
		release(i)
		fworkflow.WaitForHistoryEvent(t, ctx, cl, id, fworkflow.IsTaskCompletedFor(int32(i)))
	}

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.JSONEq(t, `[0,1,2,3,4]`, meta.GetOutput().GetValue())
	assert.ElementsMatch(t, []int{0, 1, 2, 3, 4}, l.called.Slice())
}
