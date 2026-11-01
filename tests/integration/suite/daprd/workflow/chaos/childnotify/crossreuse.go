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

package childnotify

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(crossreuse))
}

// crossreuse continues a parent as new while the cross-app child its first
// generation created completes, so the child's completion notification waits
// on the parent's lock while the parent's next turn recreates the child under
// the same ID and waits on the child's lock. The notification's bounded
// delivery breaks the cycle once. The child must then accept the recreate
// from the parent's later execution, which drops the old completion anyway:
// refusing it until the notification lands crosses the two again on every
// retry.
type crossreuse struct {
	workflow *workflow.Workflow
}

func (c *crossreuse) Setup(t *testing.T) []framework.Option {
	// Shrink the notification's delivery bound below its 5s default to keep
	// the run short, and keep the janitor period below that bound, so under
	// WorkflowsFastPath the janitor fires queue behind both locks while they
	// are crossed.
	env := func() daprd.Option {
		return daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_WORKFLOW_TEST_PARENT_NOTIFY_TIMEOUT", "2s",
			"DAPR_WORKFLOW_JANITOR_PERIOD", "1s",
		))
	}
	c.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, env()),
		workflow.WithDaprdOptions(1, env()),
	)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *crossreuse) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	const parentID = "crossreuse-p"
	const childID = "crossreuse-c"
	childApp := c.workflow.DaprN(1).AppID()

	var held atomic.Bool
	turnHeld := make(chan struct{})
	releaseTurn := make(chan struct{})
	releaseChild := make(chan struct{})
	var releaseTurnOnce, releaseChildOnce sync.Once
	t.Cleanup(func() {
		releaseTurnOnce.Do(func() { close(releaseTurn) })
		releaseChildOnce.Do(func() { close(releaseChild) })
	})

	require.NoError(t, c.workflow.Registry().AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		var gen int
		if err := ctx.GetInput(&gen); err != nil {
			return nil, err
		}
		if gen == 0 {
			ctx.CallChildWorkflow("child",
				task.WithChildWorkflowAppID(childApp),
				task.WithChildWorkflowInstanceID(childID),
			)
			if err := ctx.WaitForSingleEvent("proceed", time.Minute).Await(nil); err != nil {
				return nil, err
			}
			ctx.ContinueAsNew(1)
			return nil, nil
		}
		// The first turn of generation 1 waits here, holding the parent's
		// lock, until the child's completion notification waits on it.
		if held.CompareAndSwap(false, true) {
			close(turnHeld)
			<-releaseTurn
		}
		var out string
		if err := ctx.CallChildWorkflow("child",
			task.WithChildWorkflowAppID(childApp),
			task.WithChildWorkflowInstanceID(childID),
		).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))

	childReg := c.workflow.RegistryN(1)
	require.NoError(t, childReg.AddWorkflowN("child", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallActivity("gate").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, childReg.AddActivityN("gate", func(actx task.ActivityContext) (any, error) {
		select {
		case <-releaseChild:
			return "done", nil
		case <-actx.Context().Done():
			return nil, actx.Context().Err()
		}
	}))

	parentClient := c.workflow.BackendClient(t, ctx)
	childClient := c.workflow.BackendClientN(t, ctx, 1)

	_, err := parentClient.ScheduleNewWorkflow(ctx, "parent",
		api.WithInstanceID(parentID),
		api.WithInput(0),
	)
	require.NoError(t, err)
	wf.WaitForRuntimeStatus(t, ctx, childClient, childID, api.RUNTIME_STATUS_RUNNING)

	require.NoError(t, parentClient.RaiseEvent(ctx, parentID, "proceed"))
	select {
	case <-turnHeld:
	case <-time.After(time.Second * 20):
		require.Fail(t, "the parent's second generation never ran")
	}

	// The child commits its completion before it notifies, under its own
	// lock: COMPLETED means the notification is waiting on the parent's lock.
	releaseChildOnce.Do(func() { close(releaseChild) })
	wf.WaitForRuntimeStatus(t, ctx, childClient, childID, api.RUNTIME_STATUS_COMPLETED)
	releaseTurnOnce.Do(func() { close(releaseTurn) })

	// The recreate waits on the child's lock until the notification gives up
	// after its 2s bound. One more notification attempt then frees the ID.
	waitCtx, cancel := context.WithTimeout(ctx, time.Second*10)
	t.Cleanup(cancel)
	meta, err := parentClient.WaitForWorkflowCompletion(waitCtx, parentID)
	require.NoError(t, err, "the parent and child livelocked on each other's lock")
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), meta.GetRuntimeStatus().String())
	assert.JSONEq(t, `"done"`, meta.GetOutput().GetValue())
}
