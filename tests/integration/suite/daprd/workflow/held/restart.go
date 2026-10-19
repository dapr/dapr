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
	suite.Register(new(restart))
}

// restart terminates a running workflow and starts it again under the same
// instance ID while no worker is connected, then raises the event the new
// run waits for. They replay in the order requested: the start waits for the
// termination to be applied, then creates the new run, which gets the event.
type restart struct {
	workflow *workflow.Workflow
}

func (r *restart) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t,
		workflow.WithHeldOperations(),
	)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *restart) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, r.workflow.Registry().AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		if err := ctx.WaitForSingleEvent("go", time.Hour).Await(nil); err != nil {
			return nil, err
		}
		return input, nil
	}))

	worker := r.workflow.ConnectWorker(t, ctx, r.workflow.Registry())
	mgmt := r.workflow.ManagementClient(t, ctx)

	id, err := mgmt.ScheduleNewWorkflow(ctx, "wf", api.WithInstanceID("held-restart"), api.WithInput("first"))
	require.NoError(t, err)
	_, err = mgmt.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	worker.Disconnect(t)
	r.workflow.WaitForNoConnectedWorkers(t, ctx)

	octx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()
	require.NoError(t, mgmt.TerminateWorkflow(octx, id))
	_, err = mgmt.ScheduleNewWorkflow(octx, "wf", api.WithInstanceID(id), api.WithInput("second"))
	require.NoError(t, err)
	require.NoError(t, mgmt.RaiseEvent(octx, id, "go"))

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.Len(col, heldKeys(t, ctx, r.workflow, id), 3)
	}, time.Second*10, time.Millisecond*10)

	r.workflow.ConnectWorker(t, ctx, r.workflow.Registry())

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		meta, err := mgmt.FetchWorkflowMetadata(ctx, id, api.WithFetchPayloads(true))
		if assert.NoError(col, err) {
			assert.Equal(col, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())
			assert.Equal(col, `"second"`, meta.GetOutput().GetValue())
		}
	}, time.Second*30, time.Millisecond*100)
}
