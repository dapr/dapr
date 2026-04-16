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

package retries

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
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(childworkflow))
}

type childworkflow struct {
	workflow *workflow.Workflow
}

func (e *childworkflow) Setup(t *testing.T) []framework.Option {
	e.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *childworkflow) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	e.workflow.Registry().AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		err := ctx.CallChildWorkflow("child", task.WithChildWorkflowRetryPolicy(&task.RetryPolicy{
			MaxAttempts:          3,
			InitialRetryInterval: 10 * time.Millisecond,
		})).Await(nil)
		if err != nil {
			return nil, err
		}
		return nil, nil
	})

	var childCalls atomic.Int64
	e.workflow.Registry().AddWorkflowN("child", func(ctx *task.WorkflowContext) (any, error) {
		if childCalls.Add(1) == 1 {
			return nil, errors.New("child failure")
		}
		return nil, nil
	})

	cl := e.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "parent")
	require.NoError(t, err)

	_, err = cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	hist, err := cl.GetInstanceHistory(ctx, id)
	require.NoError(t, err)

	var childWorkflowRetryTimers int
	for _, ev := range hist.GetEvents() {
		tc := ev.GetTimerCreated()
		if tc == nil {
			continue
		}
		cwr := tc.GetChildWorkflowRetry()
		if cwr == nil {
			continue
		}
		childWorkflowRetryTimers++
		assert.NotEmpty(t, cwr.GetInstanceId())
	}
	// Child fails on first attempt, succeeds on second, so there should be
	// exactly 1 retry timer.
	assert.Equal(t, 1, childWorkflowRetryTimers)
}
