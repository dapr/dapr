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

package fallback

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
// connected. The SDK client returns as soon as a fallback host accepts each,
// rather than waiting for a state change no worker can make yet, and each
// applies once a worker connects. It covers the switch from a daprd with a
// worker back to a fallback host when the last worker disconnects.
type control struct {
	workflow *workflow.Workflow
}

func (c *control) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t,
		workflow.WithStagedOperations(),
		withSettle(t, 0),
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
	octx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()

	var terminated api.InstanceID
	require.EventuallyWithT(t, func(col *assert.CollectT) {
		var err error
		terminated, err = mgmt.ScheduleNewWorkflow(octx, "wf", api.WithInstanceID("fallback-terminate"))
		assert.NoError(col, err)
	}, time.Second*5, time.Millisecond*50)
	require.NoError(t, mgmt.TerminateWorkflow(octx, terminated))

	suspended, err := mgmt.ScheduleNewWorkflow(octx, "wf", api.WithInstanceID("fallback-suspend"))
	require.NoError(t, err)
	require.NoError(t, mgmt.SuspendWorkflow(octx, suspended, "fallback"))

	worker := c.workflow.ConnectWorker(t, ctx, c.workflow.Registry())

	meta, err := mgmt.WaitForWorkflowCompletion(ctx, terminated)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_TERMINATED, meta.GetRuntimeStatus())

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		smeta, serr := mgmt.FetchWorkflowMetadata(ctx, suspended)
		if assert.NoError(col, serr) {
			assert.Equal(col, protos.OrchestrationStatus_ORCHESTRATION_STATUS_SUSPENDED, smeta.GetRuntimeStatus())
		}
	}, time.Second*10, time.Millisecond*10)

	// With the worker gone again, the daprd is a fallback host once more:
	// resume and the event it waits for are accepted and apply once a worker
	// connects.
	worker.Disconnect(t)
	c.workflow.WaitForNoConnectedWorkers(t, ctx)

	octx, cancel = context.WithTimeout(ctx, time.Second*10)
	defer cancel()
	require.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.NoError(col, mgmt.ResumeWorkflow(octx, suspended, "fallback"))
	}, time.Second*5, time.Millisecond*50)
	require.NoError(t, mgmt.RaiseEvent(octx, suspended, "go"))

	c.workflow.ConnectWorker(t, ctx, c.workflow.Registry())

	meta, err = mgmt.WaitForWorkflowCompletion(ctx, suspended)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())
}
