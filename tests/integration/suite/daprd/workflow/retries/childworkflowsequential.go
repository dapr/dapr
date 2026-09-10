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
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(childworkflowsequential))
}

// childworkflowsequential verifies that calling a child workflow with the same
// name twice in sequence is supported and that each invocation gets its own,
// correctly correlated retry chain. The child fails on odd invocations and
// succeeds on even ones; because the calls are sequential each child is
// attempted twice (fail then succeed), producing two independent two-attempt
// retry chains that must not be conflated despite sharing the same name.
type childworkflowsequential struct {
	workflow *workflow.Workflow
}

func (e *childworkflowsequential) Setup(t *testing.T) []framework.Option {
	e.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *childworkflowsequential) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	retry := task.WithChildWorkflowRetryPolicy(&task.RetryPolicy{
		MaxAttempts:          3,
		InitialRetryInterval: 10 * time.Millisecond,
	})

	e.workflow.Registry().AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		// Two child workflows with the same name, run one after the other.
		if err := ctx.CallChildWorkflow("child", task.WithChildWorkflowInput("seq"), retry).Await(nil); err != nil {
			return nil, err
		}
		if err := ctx.CallChildWorkflow("child", task.WithChildWorkflowInput("seq"), retry).Await(nil); err != nil {
			return nil, err
		}
		return nil, nil
	})

	// Sequential execution gives a deterministic invocation order: the first
	// child is attempt #1 (fail) then #2 (succeed); the second child is
	// attempt #3 (fail) then #4 (succeed). Failing on odd invocations therefore
	// makes each sequential child fail exactly once before succeeding.
	var calls atomic.Int64
	e.workflow.Registry().AddWorkflowN("child", func(ctx *task.WorkflowContext) (any, error) {
		if calls.Add(1)%2 == 1 {
			return nil, errors.New("child failure on odd attempt")
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

	// Each of the two sequential children retries once, so there are 2 retry
	// timers in total.
	var childWorkflowRetryTimers int
	for _, ev := range hist.GetEvents() {
		if cwr := ev.GetTimerCreated().GetChildWorkflowRetry(); cwr != nil {
			childWorkflowRetryTimers++
			assert.NotEmpty(t, cwr.GetInstanceId())
		}
	}
	assert.Equal(t, 2, childWorkflowRetryTimers)

	// Two distinct retry chains, one per sequential call, each with a first
	// attempt and a single retry. The chains must have different correlation
	// keys even though both children share the same name.
	chains := fworkflow.ChildWorkflowRetryChains(hist.GetEvents())
	require.Len(t, chains, 2)
	for key, chain := range chains {
		require.Lenf(t, chain, 2, "chain %q should have one initial attempt and one retry", key)
		for _, cc := range chain {
			assert.Equal(t, "child", cc.GetName())
			assert.Equal(t, `"seq"`, cc.GetInput().GetValue())
		}
	}
}
