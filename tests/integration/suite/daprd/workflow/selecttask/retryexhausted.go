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
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(retryexhausted))
}

// retryexhausted Selects over a retry-wrapped activity that fails every
// attempt and a long timer. The exhausted chain wins, awaiting it returns the
// last attempt's error, and the chain's TaskExecutionId is the attempts' id.
type retryexhausted struct {
	workflow *workflow.Workflow
	calls    atomic.Int64
}

func (r *retryexhausted) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *retryexhausted) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	r.workflow.Registry().AddWorkflowN("retryexhausted", func(ctx *task.WorkflowContext) (any, error) {
		broken := ctx.CallActivity("broken", task.WithActivityRetryPolicy(&task.RetryPolicy{
			MaxAttempts:          2,
			InitialRetryInterval: time.Millisecond * 10,
		}))
		timer := ctx.CreateTimer(time.Second * 60)

		winner, err := ctx.Select(broken, timer)
		if err != nil {
			return nil, err
		}
		if winner != 0 {
			return nil, fmt.Errorf("expected the exhausted chain to win, got index %d", winner)
		}
		err = broken.Await(nil)
		if err == nil {
			return nil, errors.New("expected the exhausted chain to fail")
		}
		ctx.SetCustomStatus(broken.TaskExecutionId())
		return nil, err
	})
	r.workflow.Registry().AddActivityN("broken", func(task.ActivityContext) (any, error) {
		r.calls.Add(1)
		return nil, errors.New("broken activity")
	})

	cl := r.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "retryexhausted")
	require.NoError(t, err)

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_FAILED", meta.GetRuntimeStatus().String())
	assert.Contains(t, meta.GetFailureDetails().GetErrorMessage(), "broken activity")
	assert.Equal(t, int64(2), r.calls.Load())

	execID := meta.GetCustomStatus().GetValue()
	assert.NotEmpty(t, execID, "an exhausted chain must expose the execution id of its attempts")

	hist, err := cl.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, []string{execID, execID}, fworkflow.ScheduledExecIDs(hist.GetEvents(), "broken"))
	assert.Equal(t, []string{execID}, fworkflow.ActivityRetryTimerExecIDs(hist.GetEvents()))
}
