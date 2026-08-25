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
	suite.Register(new(auto))
}

type auto struct {
	workflow *workflow.Workflow
}

func (a *auto) Setup(t *testing.T) []framework.Option {
	a.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(a.workflow),
	}
}

func (a *auto) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	const parentID = "childid-auto"

	var inActivity atomic.Bool
	var blockerID atomic.Value
	releaseCh := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-releaseCh:
		default:
			close(releaseCh)
		}
	})

	reg := a.workflow.Registry()

	reg.AddWorkflowN("blocker", func(ctx *task.WorkflowContext) (any, error) {
		blockerID.Store(string(ctx.ID))
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
			ctx.CallChildWorkflow("blocker")
			ctx.WaitForSingleEvent("proceed", time.Minute).Await(nil)
			ctx.ContinueAsNew(1)
			return nil, nil
		}
		var out string
		if err := ctx.CallChildWorkflow("quick").Await(nil); err != nil {
			out = err.Error()
		}
		return out, nil
	})

	client := a.workflow.BackendClient(t, ctx)

	_, err := client.ScheduleNewWorkflow(ctx, "parent",
		api.WithInstanceID(parentID),
		api.WithInput(0),
	)
	require.NoError(t, err)
	require.Eventually(t, inActivity.Load, time.Second*10, time.Millisecond*10)

	require.NoError(t, client.RaiseEvent(ctx, parentID, "proceed"))

	waitCtx, cancel := context.WithTimeout(ctx, time.Second*30)
	t.Cleanup(cancel)
	meta, err := client.WaitForWorkflowCompletion(waitCtx, parentID)
	require.NoError(t, err, "parent deadlocked: colliding auto-generated child ID across ContinueAsNew generations was silently deduplicated")
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), meta.GetRuntimeStatus().String())
	assert.Contains(t, meta.GetOutput().GetValue(), "already exists")

	close(releaseCh)
	childID, ok := blockerID.Load().(string)
	require.True(t, ok)
	ometa, err := client.WaitForWorkflowCompletion(ctx, api.InstanceID(childID))
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), ometa.GetRuntimeStatus().String())
	assert.Equal(t, "blocker", ometa.GetName())
}
