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

package childid

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(retry))
}

type retry struct {
	workflow *workflow.Workflow
}

func (r *retry) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *retry) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	const parentID = "childid-retry"
	const pinnedID = parentID + "-pinned"

	var inActivity atomic.Bool
	var childFailed atomic.Bool
	releaseCh := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-releaseCh:
		default:
			close(releaseCh)
		}
	})

	reg := r.workflow.Registry()

	reg.AddWorkflowN("blocker", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity("block").Await(nil)
	})
	reg.AddActivityN("block", func(actx task.ActivityContext) (any, error) {
		inActivity.Store(true)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-releaseCh:
			return nil, nil
		}
	})
	reg.AddWorkflowN("quick", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	})
	reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		var gen int
		if err := ctx.GetInput(&gen); err != nil {
			return nil, err
		}
		if gen == 0 {
			ctx.CallChildWorkflow("blocker", task.WithChildWorkflowInstanceID(pinnedID))
			ctx.WaitForSingleEvent("proceed", time.Minute).Await(nil)
			ctx.ContinueAsNew(1)
			return nil, nil
		}
		return nil, ctx.CallChildWorkflow("quick",
			task.WithChildWorkflowInstanceID(pinnedID),
			task.WithChildWorkflowRetryPolicy(&task.RetryPolicy{
				MaxAttempts:          20,
				InitialRetryInterval: time.Millisecond * 500,
				Handle: func(err error) bool {
					childFailed.Store(true)
					return true
				},
			}),
		).Await(nil)
	})

	client := r.workflow.BackendClient(t, ctx)

	_, err := client.ScheduleNewWorkflow(ctx, "parent",
		api.WithInstanceID(parentID),
		api.WithInput(0),
	)
	require.NoError(t, err)
	require.Eventually(t, inActivity.Load, time.Second*10, time.Millisecond*10)

	require.NoError(t, client.RaiseEvent(ctx, parentID, "proceed"))

	require.Eventually(t, childFailed.Load, time.Second*10, time.Millisecond*10,
		"the colliding creation must fault the retry attempt, not silently deduplicate it")
	close(releaseCh)

	waitCtx, cancel := context.WithTimeout(ctx, time.Second*30)
	t.Cleanup(cancel)
	meta, err := client.WaitForWorkflowCompletion(waitCtx, parentID)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), meta.GetRuntimeStatus().String())

	ometa, err := client.FetchWorkflowMetadata(ctx, api.InstanceID(pinnedID))
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), ometa.GetRuntimeStatus().String())
	assert.Equal(t, "quick", ometa.GetName())
}
