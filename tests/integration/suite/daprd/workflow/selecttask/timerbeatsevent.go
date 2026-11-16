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
	suite.Register(new(timerbeatsevent))
}

// timerbeatsevent races an indefinite external event wait against a short
// timer. The timer wins, and an event raised after completion is harmless.
type timerbeatsevent struct {
	workflow *workflow.Workflow
}

func (e *timerbeatsevent) Setup(t *testing.T) []framework.Option {
	e.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *timerbeatsevent) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	e.workflow.Registry().AddWorkflowN("timerbeatsevent", func(ctx *task.WorkflowContext) (any, error) {
		event := ctx.WaitForSingleEvent("never", -1)
		timeout := ctx.CreateTimer(time.Second)

		winner, err := ctx.Select(event, timeout)
		if err != nil {
			return nil, err
		}
		if winner != 1 {
			return nil, fmt.Errorf("expected the timer to win, got index %d", winner)
		}
		if err := timeout.Await(nil); err != nil {
			return nil, err
		}
		return "timeout", nil
	})

	cl := e.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "timerbeatsevent")
	require.NoError(t, err)

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.JSONEq(t, `"timeout"`, meta.GetOutput().GetValue())

	// The losing event wait is still registered in the completed history. A
	// late event for it must not disturb the completed instance.
	require.NoError(t, cl.RaiseEvent(ctx, id, "never"))
	time.Sleep(time.Second)
	meta, err = cl.FetchWorkflowMetadata(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.JSONEq(t, `"timeout"`, meta.GetOutput().GetValue())
}
