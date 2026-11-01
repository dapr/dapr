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
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(eventtimeoutwins))
}

// eventtimeoutwins Selects over an event wait with its own timeout and a long
// timer. The wait's timeout fires first, so Select returns the wait's index
// and awaiting it yields ErrTaskCanceled.
type eventtimeoutwins struct {
	workflow *workflow.Workflow
}

func (e *eventtimeoutwins) Setup(t *testing.T) []framework.Option {
	e.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *eventtimeoutwins) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	e.workflow.Registry().AddWorkflowN("eventtimeoutwins", func(ctx *task.WorkflowContext) (any, error) {
		event := ctx.WaitForSingleEvent("x", time.Second)
		timer := ctx.CreateTimer(time.Second * 60)

		winner, err := ctx.Select(event, timer)
		if err != nil {
			return nil, err
		}
		if winner != 0 {
			return nil, fmt.Errorf("expected the timed-out wait to win, got index %d", winner)
		}
		err = event.Await(nil)
		if !errors.Is(err, task.ErrTaskCanceled) {
			return nil, fmt.Errorf("expected ErrTaskCanceled from the timed-out wait, got %v", err)
		}
		return "canceled", nil
	})

	cl := e.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "eventtimeoutwins")
	require.NoError(t, err)

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.JSONEq(t, `"canceled"`, meta.GetOutput().GetValue())
}
