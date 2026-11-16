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
	suite.Register(new(loserretryadvances))
}

// loserretryadvances Selects over a short timer and a retry-wrapped activity
// whose first attempt fails. The timer wins, but Select still polls the
// losing chain on every check, so its backoff timer is armed and the chain
// completes when awaited afterwards without a replay mismatch.
type loserretryadvances struct {
	workflow *workflow.Workflow
	calls    atomic.Int64
}

func (l *loserretryadvances) Setup(t *testing.T) []framework.Option {
	l.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(l.workflow),
	}
}

func (l *loserretryadvances) Run(t *testing.T, ctx context.Context) {
	l.workflow.WaitUntilRunning(t, ctx)

	l.workflow.Registry().AddWorkflowN("loserretryadvances", func(ctx *task.WorkflowContext) (any, error) {
		timer := ctx.CreateTimer(time.Second)
		flaky := ctx.CallActivity("flaky", task.WithActivityRetryPolicy(&task.RetryPolicy{
			MaxAttempts:          3,
			InitialRetryInterval: time.Second * 3,
		}))

		winner, err := ctx.Select(timer, flaky)
		if err != nil {
			return nil, err
		}
		if winner != 0 {
			return nil, fmt.Errorf("expected the timer to win, got index %d", winner)
		}
		if err := timer.Await(nil); err != nil {
			return nil, err
		}

		var result string
		if err := flaky.Await(&result); err != nil {
			return nil, err
		}
		return result, nil
	})
	l.workflow.Registry().AddActivityN("flaky", func(task.ActivityContext) (any, error) {
		if l.calls.Add(1) == 1 {
			return nil, errors.New("first attempt fails")
		}
		return "second attempt", nil
	})

	cl := l.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "loserretryadvances")
	require.NoError(t, err)

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.JSONEq(t, `"second attempt"`, meta.GetOutput().GetValue())
	assert.Equal(t, int64(2), l.calls.Load())

	hist, err := cl.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	ids := fworkflow.ScheduledExecIDs(hist.GetEvents(), "flaky")
	require.Len(t, ids, 2)
	assert.Equal(t, ids[0], ids[1])
	assert.Equal(t, []string{ids[0]}, fworkflow.ActivityRetryTimerExecIDs(hist.GetEvents()))
}
