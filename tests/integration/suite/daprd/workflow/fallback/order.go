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
	suite.Register(new(order))
}

// order starts a workflow, then suspends and resumes it several times and
// raises the event it waits for, all while no worker is connected. Each
// operation lands in the instance's inbox in the order requested, so once a
// worker connects the workflow ends resumed and completes.
type order struct {
	workflow *workflow.Workflow
}

func (o *order) Setup(t *testing.T) []framework.Option {
	o.workflow = workflow.New(t,
		workflow.WithStagedOperations(),
		withSettle(t, 0),
	)

	return []framework.Option{
		framework.WithProcesses(o.workflow),
	}
}

func (o *order) Run(t *testing.T, ctx context.Context) {
	o.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, o.workflow.Registry().AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.WaitForSingleEvent("go", time.Hour).Await(nil)
	}))

	mgmt := o.workflow.ManagementClient(t, ctx)

	octx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()

	var id api.InstanceID
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var err error
		id, err = mgmt.ScheduleNewWorkflow(octx, "wf", api.WithInstanceID("fallback-order"))
		assert.NoError(c, err)
	}, time.Second*5, time.Millisecond*50)

	const flips = 5
	for range flips {
		require.NoError(t, mgmt.SuspendWorkflow(octx, id, "fallback"))
		require.NoError(t, mgmt.ResumeWorkflow(octx, id, "fallback"))
	}
	require.NoError(t, mgmt.RaiseEvent(octx, id, "go"))

	meta, err := mgmt.FetchWorkflowMetadata(octx, id)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_PENDING, meta.GetRuntimeStatus())

	o.workflow.ConnectWorker(t, ctx, o.workflow.Registry())

	wctx, wcancel := context.WithTimeout(ctx, time.Second*30)
	defer wcancel()
	meta, err = mgmt.WaitForWorkflowCompletion(wctx, id)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())
}
