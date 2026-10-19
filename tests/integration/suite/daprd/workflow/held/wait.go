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
	suite.Register(new(wait))
}

// wait checks that with the feature on and a worker connected, nothing is
// held and the SDK client still waits for each operation to apply: a purge
// right after a terminate finds the instance terminated.
type wait struct {
	workflow *workflow.Workflow
}

func (w *wait) Setup(t *testing.T) []framework.Option {
	w.workflow = workflow.New(t,
		workflow.WithHeldOperations(),
	)

	return []framework.Option{
		framework.WithProcesses(w.workflow),
	}
}

func (w *wait) Run(t *testing.T, ctx context.Context) {
	w.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, w.workflow.Registry().AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.WaitForSingleEvent("go", time.Hour).Await(nil)
	}))

	// The worker's connect returns before placement disseminates the new
	// host; a start before that would be held.
	version := w.workflow.PlacementVersion(t, ctx)
	w.workflow.ConnectWorker(t, ctx, w.workflow.Registry())
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Greater(c, w.workflow.PlacementVersion(t, ctx), version)
	}, time.Second*10, time.Millisecond*10)
	mgmt := w.workflow.ManagementClient(t, ctx)

	id, err := mgmt.ScheduleNewWorkflow(ctx, "wf", api.WithInstanceID("held-wait"))
	require.NoError(t, err)
	meta, err := mgmt.FetchWorkflowMetadata(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_RUNNING, meta.GetRuntimeStatus())

	require.NoError(t, mgmt.SuspendWorkflow(ctx, id, "s"))
	meta, err = mgmt.FetchWorkflowMetadata(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_SUSPENDED, meta.GetRuntimeStatus())

	require.NoError(t, mgmt.ResumeWorkflow(ctx, id, "r"))
	meta, err = mgmt.FetchWorkflowMetadata(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_RUNNING, meta.GetRuntimeStatus())

	require.NoError(t, mgmt.TerminateWorkflow(ctx, id))
	require.NoError(t, mgmt.PurgeWorkflowState(ctx, id))

	assert.Empty(t, heldKeys(t, ctx, w.workflow, id))
}
