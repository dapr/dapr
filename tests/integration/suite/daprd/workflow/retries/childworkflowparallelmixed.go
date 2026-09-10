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
	"slices"
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
	suite.Register(new(childworkflowparallelmixed))
}

// childworkflowparallelmixed exercises retry-chain correlation for two child
// workflows that share the same name AND the same input, awaited at once so
// they run in parallel, where one fails (and is retried) while the other does
// not. Because identical name+input children are indistinguishable to the child
// function, and each retry attempt is a brand new instance, the outcome is
// driven purely by execution order: only the globally first attempt fails, so
// exactly one of the two children is retried once and the other succeeds
// immediately. The test asserts the correlation still produces one two-attempt
// chain and one single-attempt chain, with distinct, non-crossed instance IDs.
type childworkflowparallelmixed struct {
	workflow *workflow.Workflow
}

func (e *childworkflowparallelmixed) Setup(t *testing.T) []framework.Option {
	e.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *childworkflowparallelmixed) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	retry := task.WithChildWorkflowRetryPolicy(&task.RetryPolicy{
		MaxAttempts:          3,
		InitialRetryInterval: 10 * time.Millisecond,
	})

	e.workflow.Registry().AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		// Schedule both children before awaiting either, so they run in
		// parallel. Same name, same input.
		c1 := ctx.CallChildWorkflow("child", task.WithChildWorkflowInput("same-input"), retry)
		c2 := ctx.CallChildWorkflow("child", task.WithChildWorkflowInput("same-input"), retry)
		return nil, errors.Join(c1.Await(nil), c2.Await(nil))
	})

	// Only the globally first attempt fails; every later attempt (the retry of
	// whichever child failed, and the other child's single attempt) succeeds.
	// The result is deterministic in shape regardless of scheduling: exactly one
	// child is retried once and the other succeeds on its first attempt.
	var calls atomic.Int64
	e.workflow.Registry().AddWorkflowN("child", func(ctx *task.WorkflowContext) (any, error) {
		if calls.Add(1) == 1 {
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

	// Exactly one child is retried, exactly once.
	var childWorkflowRetryTimers int
	for _, ev := range hist.GetEvents() {
		if cwr := ev.GetTimerCreated().GetChildWorkflowRetry(); cwr != nil {
			childWorkflowRetryTimers++
			assert.NotEmpty(t, cwr.GetInstanceId())
		}
	}
	assert.Equal(t, 1, childWorkflowRetryTimers)

	// Two distinct chains: the retried child has two attempts, the other has a
	// single attempt. Both share the same name and input.
	chains := fworkflow.ChildWorkflowRetryChains(hist.GetEvents())
	require.Len(t, chains, 2)

	lengths := make([]int, 0, len(chains))
	for _, chain := range chains {
		lengths = append(lengths, len(chain))
		for _, cc := range chain {
			assert.Equal(t, "child", cc.GetName())
			assert.Equal(t, `"same-input"`, cc.GetInput().GetValue())
		}
	}
	// One child is retried once (a two-attempt chain); the other succeeds on its
	// first attempt (a single-attempt chain).
	slices.Sort(lengths)
	assert.Equal(t, []int{1, 2}, lengths)
}
