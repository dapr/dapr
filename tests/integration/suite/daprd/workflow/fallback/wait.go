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
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(wait))
}

// wait checks that with the feature on and a worker connected, the SDK
// client still waits for each operation to apply: a purge right after a
// terminate finds the instance terminated. The fallback skip-wait must not
// fire on a daprd with a worker, both on one that has just switched from a
// fallback host and on one that switched back and forth again.
type wait struct {
	workflow *workflow.Workflow
}

func (w *wait) Setup(t *testing.T) []framework.Option {
	w.workflow = workflow.New(t,
		workflow.WithStagedOperations(),
		withSettle(t, 0),
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

	mgmt := w.workflow.ManagementClient(t, ctx)

	// The daprd serves a start as a fallback host first.
	octx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()
	var first api.InstanceID
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var err error
		first, err = mgmt.ScheduleNewWorkflow(octx, "wf", api.WithInstanceID("fallback-wait-first"))
		assert.NoError(c, err)
	}, time.Second*5, time.Millisecond*50)
	meta, err := mgmt.FetchWorkflowMetadata(octx, first)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_PENDING, meta.GetRuntimeStatus())

	// The worker's connect returns before the daprd takes the workflow
	// reminders; the staged start running shows the switch has landed.
	worker := w.workflow.ConnectWorker(t, ctx, w.workflow.Registry())
	_, err = mgmt.WaitForWorkflowStart(ctx, first)
	require.NoError(t, err)

	checkWaits(t, ctx, mgmt, "fallback-wait-active")

	// Back to a fallback host, then a worker again.
	worker.Disconnect(t)
	w.workflow.WaitForNoConnectedWorkers(t, ctx)

	octx, cancel = context.WithTimeout(ctx, time.Second*10)
	defer cancel()
	var second api.InstanceID
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var serr error
		second, serr = mgmt.ScheduleNewWorkflow(octx, "wf", api.WithInstanceID("fallback-wait-second"))
		assert.NoError(c, serr)
	}, time.Second*5, time.Millisecond*50)
	meta, err = mgmt.FetchWorkflowMetadata(octx, second)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_PENDING, meta.GetRuntimeStatus())

	w.workflow.ConnectWorker(t, ctx, w.workflow.Registry())
	_, err = mgmt.WaitForWorkflowStart(ctx, second)
	require.NoError(t, err)

	checkWaits(t, ctx, mgmt, "fallback-wait-reactive")
}

// checkWaits runs each operation against a daprd with a worker and reads the
// state right after it, which only holds if the client waited for it to apply.
func checkWaits(t *testing.T, ctx context.Context, mgmt *client.TaskHubGrpcClient, id api.InstanceID) {
	t.Helper()

	_, err := mgmt.ScheduleNewWorkflow(ctx, "wf", api.WithInstanceID(id))
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

	// A purge fails on an instance that is not yet terminated.
	require.NoError(t, mgmt.TerminateWorkflow(ctx, id))
	require.NoError(t, mgmt.PurgeWorkflowState(ctx, id))
	_, err = mgmt.FetchWorkflowMetadata(ctx, id)
	require.ErrorIs(t, err, api.ErrInstanceNotFound)
}
