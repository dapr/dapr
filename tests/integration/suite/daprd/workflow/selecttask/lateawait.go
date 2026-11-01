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
	suite.Register(new(lateawait))
}

// lateawait schedules a retry-wrapped activity but does not poll it until a
// separate timer has fired. The first attempt fails long before then; the
// backoff timer is only created once the chain is first polled, and the
// chain then completes normally.
type lateawait struct {
	workflow *workflow.Workflow
	calls    atomic.Int64
}

func (l *lateawait) Setup(t *testing.T) []framework.Option {
	l.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(l.workflow),
	}
}

func (l *lateawait) Run(t *testing.T, ctx context.Context) {
	l.workflow.WaitUntilRunning(t, ctx)

	l.workflow.Registry().AddWorkflowN("lateawait", func(ctx *task.WorkflowContext) (any, error) {
		flaky := ctx.CallActivity("flaky", task.WithActivityRetryPolicy(&task.RetryPolicy{
			MaxAttempts:          3,
			InitialRetryInterval: time.Millisecond * 10,
		}))
		if err := ctx.CreateTimer(time.Second * 2).Await(nil); err != nil {
			return nil, err
		}

		winner, err := ctx.Select(flaky)
		if err != nil {
			return nil, err
		}
		if winner != 0 {
			return nil, errors.New("expected index 0 from a single candidate")
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
	id, err := cl.ScheduleNewWorkflow(ctx, "lateawait")
	require.NoError(t, err)

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.JSONEq(t, `"second attempt"`, meta.GetOutput().GetValue())
	assert.Equal(t, int64(2), l.calls.Load())

	hist, err := cl.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	assert.Len(t, fworkflow.ScheduledExecIDs(hist.GetEvents(), "flaky"), 2)

	// The backoff timer is created after the plain timer fired, because the
	// chain was not polled before then.
	var plainFired, retryCreated int
	for i, ev := range hist.GetEvents() {
		if tc := ev.GetTimerCreated(); tc != nil && tc.GetActivityRetry() != nil {
			retryCreated = i
		}
		if ev.GetTimerFired() != nil && plainFired == 0 {
			plainFired = i
		}
	}
	assert.Positive(t, retryCreated, "expected an activity retry timer in history")
	assert.Greater(t, retryCreated, plainFired, "the retry timer must be created after the plain timer fired")
}
