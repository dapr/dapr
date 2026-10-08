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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(alreadycompleted))
}

// alreadycompleted Selects over tasks that have both completed before Select
// is called. The lowest index wins in both argument orders, and nothing is
// scheduled again.
type alreadycompleted struct {
	workflow *workflow.Workflow
}

func (a *alreadycompleted) Setup(t *testing.T) []framework.Option {
	a.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(a.workflow),
	}
}

func (a *alreadycompleted) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	a.workflow.Registry().AddWorkflowN("alreadycompleted", func(ctx *task.WorkflowContext) (any, error) {
		first := ctx.CallActivity("noop", task.WithActivityInput("a"))
		second := ctx.CallActivity("noop", task.WithActivityInput("b"))

		if err := ctx.CallActivity("noop", task.WithActivityInput("c")).Await(nil); err != nil {
			return nil, err
		}

		// Both completions are in history before either Select below runs.
		if err := first.Await(nil); err != nil {
			return nil, err
		}
		if err := second.Await(nil); err != nil {
			return nil, err
		}

		ab, err := ctx.Select(first, second)
		if err != nil {
			return nil, err
		}
		ba, err := ctx.Select(second, first)
		if err != nil {
			return nil, err
		}
		return []int{ab, ba}, nil
	})
	a.workflow.Registry().AddActivityN("noop", func(ctx task.ActivityContext) (any, error) {
		var in string
		return in, ctx.GetInput(&in)
	})

	cl := a.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "alreadycompleted")
	require.NoError(t, err)

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.JSONEq(t, `[0,0]`, meta.GetOutput().GetValue())
	assert.Equal(t, 3, fworkflow.CountHistoryEventsOfType[protos.HistoryEvent_TaskScheduled](t, ctx, cl, id))
}
