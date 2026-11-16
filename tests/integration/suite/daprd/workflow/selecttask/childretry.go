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
	suite.Register(new(childretry))
}

// childretry Selects over a retry-wrapped child workflow that fails twice
// before succeeding and a long timer. The child's retry chain wins and the
// backoff timers carry the child workflow retry origin.
type childretry struct {
	workflow *workflow.Workflow
	calls    atomic.Int64
}

func (c *childretry) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *childretry) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	c.workflow.Registry().AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		child := ctx.CallChildWorkflow("flakychild", task.WithChildWorkflowRetryPolicy(&task.RetryPolicy{
			MaxAttempts:          3,
			InitialRetryInterval: time.Millisecond * 10,
		}))
		timer := ctx.CreateTimer(time.Second * 60)

		winner, err := ctx.Select(child, timer)
		if err != nil {
			return nil, err
		}
		if winner != 0 {
			return nil, fmt.Errorf("expected the child chain to win, got index %d", winner)
		}
		var out string
		if err := child.Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	})
	c.workflow.Registry().AddWorkflowN("flakychild", func(ctx *task.WorkflowContext) (any, error) {
		if c.calls.Add(1) < 3 {
			return nil, errors.New("child failed")
		}
		return "child ok", nil
	})

	cl := c.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "parent")
	require.NoError(t, err)

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.JSONEq(t, `"child ok"`, meta.GetOutput().GetValue())
	assert.Equal(t, int64(3), c.calls.Load())
	assert.Equal(t, 3, fworkflow.CountHistoryEventsOfType[protos.HistoryEvent_ChildWorkflowInstanceCreated](t, ctx, cl, id))

	hist, err := cl.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	var childRetryTimers int
	for _, ev := range hist.GetEvents() {
		if ev.GetTimerCreated().GetChildWorkflowRetry() != nil {
			childRetryTimers++
		}
	}
	assert.Equal(t, 2, childRetryTimers)
}
