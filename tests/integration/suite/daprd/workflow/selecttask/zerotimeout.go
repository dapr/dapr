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
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(zerotimeout))
}

// zerotimeout Selects over a zero-timeout event wait, which is canceled on
// creation when no event is buffered, and a long timer. Select returns the
// canceled wait immediately without waiting on any history.
type zerotimeout struct {
	workflow *workflow.Workflow
}

func (z *zerotimeout) Setup(t *testing.T) []framework.Option {
	z.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(z.workflow),
	}
}

func (z *zerotimeout) Run(t *testing.T, ctx context.Context) {
	z.workflow.WaitUntilRunning(t, ctx)

	z.workflow.Registry().AddWorkflowN("zerotimeout", func(ctx *task.WorkflowContext) (any, error) {
		event := ctx.WaitForSingleEvent("x", 0)
		timer := ctx.CreateTimer(time.Second * 60)

		winner, err := ctx.Select(event, timer)
		if err != nil {
			return nil, err
		}
		if winner != 0 {
			return nil, fmt.Errorf("expected the zero-timeout wait to win, got index %d", winner)
		}
		err = event.Await(nil)
		if !errors.Is(err, task.ErrTaskCanceled) {
			return nil, fmt.Errorf("expected ErrTaskCanceled from the zero-timeout wait, got %v", err)
		}
		return "canceled", nil
	})

	cl := z.workflow.BackendClient(t, ctx)
	start := time.Now()
	id, err := cl.ScheduleNewWorkflow(ctx, "zerotimeout")
	require.NoError(t, err)

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.JSONEq(t, `"canceled"`, meta.GetOutput().GetValue())
	assert.Less(t, time.Since(start), time.Second*10, "a zero-timeout wait must not block on any timer")
	// Only the losing 60s timer is recorded; a zero-timeout wait creates none.
	assert.Equal(t, 1, fworkflow.CountHistoryEventsOfType[protos.HistoryEvent_TimerCreated](t, ctx, cl, id))
	assert.Equal(t, 0, fworkflow.CountHistoryEventsOfType[protos.HistoryEvent_TimerFired](t, ctx, cl, id))
}
