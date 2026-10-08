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
	"encoding/json"
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
	suite.Register(new(retrywinner))
}

// retrywinner Selects over a retry-wrapped activity that fails twice before
// succeeding and a long timer. The retry chain wins, and its TaskExecutionId
// is the id recorded on every attempt and backoff timer in history.
type retrywinner struct {
	workflow *workflow.Workflow
	calls    atomic.Int64
}

func (r *retrywinner) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *retrywinner) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	r.workflow.Registry().AddWorkflowN("retrywinner", func(ctx *task.WorkflowContext) (any, error) {
		flaky := ctx.CallActivity("flaky", task.WithActivityRetryPolicy(&task.RetryPolicy{
			MaxAttempts:          3,
			InitialRetryInterval: time.Millisecond * 10,
		}))
		timer := ctx.CreateTimer(time.Second * 60)

		winner, err := ctx.Select(flaky, timer)
		if err != nil {
			return nil, err
		}
		if winner != 0 {
			return nil, fmt.Errorf("expected the retry chain to win, got index %d", winner)
		}
		var result string
		if err := flaky.Await(&result); err != nil {
			return nil, err
		}
		return []string{result, flaky.TaskExecutionId()}, nil
	})
	r.workflow.Registry().AddActivityN("flaky", func(task.ActivityContext) (any, error) {
		if r.calls.Add(1) < 3 {
			return nil, errors.New("flaky failure")
		}
		return "third time lucky", nil
	})

	cl := r.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "retrywinner")
	require.NoError(t, err)

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.Equal(t, int64(3), r.calls.Load())

	var out []string
	require.NoError(t, json.Unmarshal([]byte(meta.GetOutput().GetValue()), &out))
	require.Len(t, out, 2)
	assert.Equal(t, "third time lucky", out[0])
	execID := out[1]
	assert.NotEmpty(t, execID, "a retried task must expose the execution id of its attempts")

	hist, err := cl.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, []string{execID, execID, execID}, fworkflow.ScheduledExecIDs(hist.GetEvents(), "flaky"))
	assert.Equal(t, []string{execID, execID}, fworkflow.ActivityRetryTimerExecIDs(hist.GetEvents()))
}
