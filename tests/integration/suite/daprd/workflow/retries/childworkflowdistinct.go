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
	suite.Register(new(childworkflowdistinct))
}

// childworkflowdistinct exercises retry-chain correlation for two child
// workflows with different names AND different inputs, awaited at once so they
// run in parallel. One child fails twice and succeeds on its third attempt
// (producing a three-attempt retry chain); the other succeeds on its first
// attempt (a single-attempt chain with no RetryParentInstanceInfo). The test
// verifies each chain correlates only to its own first attempt and carries the
// expected name and input.
type childworkflowdistinct struct {
	workflow *workflow.Workflow
}

func (e *childworkflowdistinct) Setup(t *testing.T) []framework.Option {
	e.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *childworkflowdistinct) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	retry := task.WithChildWorkflowRetryPolicy(&task.RetryPolicy{
		MaxAttempts:          3,
		InitialRetryInterval: 10 * time.Millisecond,
	})

	e.workflow.Registry().AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		// Schedule both children before awaiting either, so they run in
		// parallel. Different names, different inputs.
		cFail := ctx.CallChildWorkflow("childFail", task.WithChildWorkflowInput("fail-input"), retry)
		cOk := ctx.CallChildWorkflow("childOk", task.WithChildWorkflowInput("ok-input"), retry)
		return nil, errors.Join(cFail.Await(nil), cOk.Await(nil))
	})

	// childFail fails its first two attempts and succeeds on the third.
	var failCalls atomic.Int64
	e.workflow.Registry().AddWorkflowN("childFail", func(ctx *task.WorkflowContext) (any, error) {
		if failCalls.Add(1) <= 2 {
			return nil, errors.New("childFail failure")
		}
		return nil, nil
	})

	// childOk succeeds on its first attempt and is never retried.
	e.workflow.Registry().AddWorkflowN("childOk", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	})

	cl := e.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "parent")
	require.NoError(t, err)

	_, err = cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	hist, err := cl.GetInstanceHistory(ctx, id)
	require.NoError(t, err)

	// Only childFail retries (twice); childOk never does.
	var childWorkflowRetryTimers int
	for _, ev := range hist.GetEvents() {
		if cwr := ev.GetTimerCreated().GetChildWorkflowRetry(); cwr != nil {
			childWorkflowRetryTimers++
			assert.NotEmpty(t, cwr.GetInstanceId())
		}
	}
	assert.Equal(t, 2, childWorkflowRetryTimers)

	chains := fworkflow.ChildWorkflowRetryChains(hist.GetEvents())
	require.Len(t, chains, 2)

	var sawFail, sawOk bool
	for key, chain := range chains {
		switch chain[0].GetName() {
		case "childFail":
			sawFail = true
			require.Lenf(t, chain, 3, "childFail chain %q should have two retries", key)
			for _, cc := range chain {
				assert.Equal(t, "childFail", cc.GetName())
				assert.Equal(t, `"fail-input"`, cc.GetInput().GetValue())
			}
		case "childOk":
			sawOk = true
			require.Lenf(t, chain, 1, "childOk chain %q should have a single attempt", key)
			assert.Equal(t, `"ok-input"`, chain[0].GetInput().GetValue())
			assert.Nil(t, chain[0].GetRetryParentInstanceInfo())
		default:
			t.Fatalf("unexpected child workflow name %q", chain[0].GetName())
		}
	}
	assert.True(t, sawFail, "expected a childFail retry chain")
	assert.True(t, sawOk, "expected a childOk chain")
}
