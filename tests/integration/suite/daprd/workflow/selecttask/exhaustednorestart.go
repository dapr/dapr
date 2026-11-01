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
	"sync/atomic"
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
	suite.Register(new(exhaustednorestart))
}

// exhaustednorestart awaits an exhausted retry chain twice and then passes it
// to another Select. Neither the second Await nor the Select may start a new
// chain: the activity runs exactly MaxAttempts times.
type exhaustednorestart struct {
	workflow *workflow.Workflow
	calls    atomic.Int64
}

func (e *exhaustednorestart) Setup(t *testing.T) []framework.Option {
	e.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *exhaustednorestart) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	e.workflow.Registry().AddWorkflowN("exhaustednorestart", func(ctx *task.WorkflowContext) (any, error) {
		broken := ctx.CallActivity("broken", task.WithActivityRetryPolicy(&task.RetryPolicy{
			MaxAttempts:          2,
			InitialRetryInterval: time.Millisecond * 10,
		}))

		first := broken.Await(nil)
		if first == nil {
			return nil, errors.New("expected the chain to be exhausted")
		}
		second := broken.Await(nil)
		if second == nil || second.Error() != first.Error() {
			return nil, fmt.Errorf("second Await must repeat the failure, got %v", second)
		}

		timer := ctx.CreateTimer(time.Millisecond * 500)
		winner, err := ctx.Select(broken, timer)
		if err != nil {
			return nil, err
		}
		if winner != 0 {
			return nil, fmt.Errorf("an already failed chain must win Select, got index %d", winner)
		}
		return "no restart", nil
	})
	e.workflow.Registry().AddActivityN("broken", func(task.ActivityContext) (any, error) {
		e.calls.Add(1)
		return nil, errors.New("broken activity")
	})

	cl := e.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "exhaustednorestart")
	require.NoError(t, err)

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.JSONEq(t, `"no restart"`, meta.GetOutput().GetValue())
	assert.Equal(t, int64(2), e.calls.Load())
	assert.Equal(t, 2, fworkflow.CountHistoryEventsOfType[protos.HistoryEvent_TaskScheduled](t, ctx, cl, id))

	hist, err := cl.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	assert.Len(t, fworkflow.ActivityRetryTimerExecIDs(hist.GetEvents()), 1)
}
