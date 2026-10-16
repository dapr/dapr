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

package held

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(control))
}

// control terminates, suspends and resumes workflows while no worker is
// connected. The SDK client returns as soon as each is held, rather than
// waiting for a state change no worker can make yet, and each applies once a
// worker connects.
type control struct {
	workflow *workflow.Workflow
}

func (c *control) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t,
		workflow.WithHeldOperations(),
	)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *control) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, c.workflow.Registry().AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.WaitForSingleEvent("go", time.Hour).Await(nil)
	}))

	mgmt := c.workflow.ManagementClient(t, ctx)

	// Each call would otherwise wait for the instance to change state, which
	// with no worker never happens.
	octx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	terminated, err := mgmt.ScheduleNewWorkflow(octx, "wf", api.WithInstanceID("held-terminate"))
	require.NoError(t, err)
	require.NoError(t, mgmt.TerminateWorkflow(octx, terminated))

	suspended, err := mgmt.ScheduleNewWorkflow(octx, "wf", api.WithInstanceID("held-suspend"))
	require.NoError(t, err)
	require.NoError(t, mgmt.SuspendWorkflow(octx, suspended, "held"))

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.Len(col, heldKeys(t, ctx, c.workflow, terminated), 2)
		assert.Len(col, heldKeys(t, ctx, c.workflow, suspended), 2)
	}, time.Second*10, time.Millisecond*10)

	worker := c.workflow.ConnectWorker(t, ctx, c.workflow.Registry())

	meta, err := mgmt.WaitForWorkflowCompletion(ctx, terminated)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_TERMINATED, meta.GetRuntimeStatus())

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		meta, err := mgmt.FetchWorkflowMetadata(ctx, suspended)
		if assert.NoError(col, err) {
			assert.Equal(col, protos.OrchestrationStatus_ORCHESTRATION_STATUS_SUSPENDED, meta.GetRuntimeStatus())
		}
	}, time.Second*10, time.Millisecond*10)

	// With the worker gone again, resume and the event it waits for are held
	// in turn.
	worker.Disconnect(t)
	c.workflow.WaitForNoConnectedWorkers(t, ctx)

	octx, cancel = context.WithTimeout(ctx, time.Second*5)
	defer cancel()
	require.NoError(t, mgmt.ResumeWorkflow(octx, suspended, "held"))
	require.NoError(t, mgmt.RaiseEvent(octx, suspended, "go"))

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.Len(col, heldKeys(t, ctx, c.workflow, suspended), 2)
	}, time.Second*10, time.Millisecond*10)

	c.workflow.ConnectWorker(t, ctx, c.workflow.Registry())

	meta, err = mgmt.WaitForWorkflowCompletion(ctx, suspended)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())
}
