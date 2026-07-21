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
	suite.Register(new(childworkflowparallel))
}

// childworkflowparallel exercises retry-chain correlation for two child
// workflows that share the same name AND the same input and are awaited at
// once, so they run in parallel. Because the two children are indistinguishable
// from the child function's point of view (identical name + input), both behave
// identically and fail every attempt. With MaxAttempts=3 each child therefore
// fails three times, producing two independent three-attempt retry chains. The
// test verifies the runtime keeps the two chains correctly correlated and never
// crosses their instance IDs even though the children look identical.
type childworkflowparallel struct {
	workflow *workflow.Workflow
}

func (e *childworkflowparallel) Setup(t *testing.T) []framework.Option {
	e.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *childworkflowparallel) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	retry := task.WithChildWorkflowRetryPolicy(&task.RetryPolicy{
		MaxAttempts:          3,
		InitialRetryInterval: 10 * time.Millisecond,
	})

	e.workflow.Registry().AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		// Schedule both children before awaiting either, so they are dispatched
		// in the same orchestration turn and run in parallel. Same name, same
		// input.
		c1 := ctx.CallChildWorkflow("child", task.WithChildWorkflowInput("same-input"), retry)
		c2 := ctx.CallChildWorkflow("child", task.WithChildWorkflowInput("same-input"), retry)
		// Both children exhaust their retries and fail; the parent swallows the
		// errors so it completes and we can inspect the recorded history.
		_ = c1.Await(nil)
		_ = c2.Await(nil)
		return nil, nil
	})

	e.workflow.Registry().AddWorkflowN("child", func(ctx *task.WorkflowContext) (any, error) {
		return nil, errors.New("child always fails")
	})

	cl := e.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "parent")
	require.NoError(t, err)

	_, err = cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	hist, err := cl.GetInstanceHistory(ctx, id)
	require.NoError(t, err)

	// Two children, each attempted 3 times and failing, produce 4 retry timers
	// (two per child).
	var childWorkflowRetryTimers int
	for _, ev := range hist.GetEvents() {
		if cwr := ev.GetTimerCreated().GetChildWorkflowRetry(); cwr != nil {
			childWorkflowRetryTimers++
			assert.NotEmpty(t, cwr.GetInstanceId())
		}
	}
	assert.Equal(t, 4, childWorkflowRetryTimers)

	// Grouping the child-created events by correlation key must yield exactly
	// two distinct chains (one per parallel child, with different keys), each
	// with three attempts carrying the identical name and input. A crossed or
	// missing RetryParentInstanceInfo would change the number of chains or their
	// lengths.
	chains := fworkflow.ChildWorkflowRetryChains(hist.GetEvents())
	require.Len(t, chains, 2)

	for key, chain := range chains {
		require.Lenf(t, chain, 3, "chain %q should have one initial attempt and two retries", key)
		for _, cc := range chain {
			assert.Equal(t, "child", cc.GetName())
			assert.Equal(t, `"same-input"`, cc.GetInput().GetValue())
		}
	}
}
