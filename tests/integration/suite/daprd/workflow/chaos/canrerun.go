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

package chaos

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/fault"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(canrerun))
}

// canrerun pins that a ContinueAsNew turn whose commit fails, and which the
// reminder therefore runs again, does not fail the workflow. The turn
// dispatches the new generation's child creation before it commits, so the
// re-run dispatches it a second time. The child must recognise the retry as
// the same parent execution rather than reject it as a collision with another
// one, which the parent would turn into a task failure.
type canrerun struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
}

func (c *canrerun) Setup(t *testing.T) []framework.Option {
	c.store = fault.New(t)

	sock := socket.New(t)
	c.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(c.store),
	)

	c.workflow = workflow.New(t,
		workflow.WithNoDB(),
		workflow.WithDaprdOptions(0,
			daprd.WithSocket(t, sock),
			daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.%s
  version: v1
  metadata:
  - name: actorStateStore
    value: "true"
`, c.ss.SocketName())),
		),
	)

	return []framework.Option{
		framework.WithProcesses(c.ss, c.workflow),
	}
}

func (c *canrerun) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	const (
		parentID    = "canrerun"
		generations = 2
	)

	var calls [generations + 1]atomic.Int32
	blocked := make(chan struct{}, 1)
	release := make(chan struct{})

	reg := c.workflow.Registry()
	require.NoError(t, reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		var gen int
		if err := ctx.GetInput(&gen); err != nil {
			return nil, err
		}
		var out int
		if err := ctx.CallChildWorkflow("child",
			task.WithChildWorkflowInput(gen),
			task.WithChildWorkflowInstanceID(fmt.Sprintf("%s-child-%d", ctx.ID, gen)),
		).Await(&out); err != nil {
			return nil, err
		}
		if out != gen {
			return nil, fmt.Errorf("child of generation %d returned %d", gen, out)
		}
		if gen < generations {
			ctx.ContinueAsNew(gen + 1)
		}
		return nil, nil
	}))
	require.NoError(t, reg.AddWorkflowN("child", func(ctx *task.WorkflowContext) (any, error) {
		var gen int
		if err := ctx.GetInput(&gen); err != nil {
			return nil, err
		}
		if err := ctx.CallActivity("step", task.WithActivityInput(gen)).Await(nil); err != nil {
			return nil, err
		}
		return gen, nil
	}))
	require.NoError(t, reg.AddActivityN("step", func(actx task.ActivityContext) (any, error) {
		var gen int
		if err := actx.GetInput(&gen); err != nil {
			return nil, err
		}
		calls[gen].Add(1)
		// Hold the first generation's child so the fault can be armed while
		// the parent is idle: the next parent turn is the one that consumes
		// the child's completion and continues as new.
		if gen == 1 {
			blocked <- struct{}{}
			select {
			case <-release:
			case <-actx.Context().Done():
				return nil, actx.Context().Err()
			}
		}
		return nil, nil
	}))

	cl := c.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "parent", api.WithInstanceID(parentID), api.WithInput(1))
	require.NoError(t, err)

	select {
	case <-blocked:
	case <-time.After(20 * time.Second):
		require.Fail(t, "the first generation's child never ran")
	}

	// The next commit that rewrites the parent's history is the ContinueAsNew
	// turn's, which has already dispatched the second generation's child by
	// the time it commits. Failing it once forces the re-run.
	failed := make(chan struct{})
	c.store.ArmFailures(parentID+"||history-", 1, failed)
	close(release)
	select {
	case <-failed:
	case <-time.After(20 * time.Second):
		require.Fail(t, "the injected commit failure never fired")
	}

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus(),
		"the re-run turn's child creation must be accepted as the same parent execution")
	for gen := 1; gen <= generations; gen++ {
		assert.Equal(t, int32(1), calls[gen].Load(), "the child of generation %d must run exactly once", gen)
	}
}
