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
	suite.Register(new(rerun))
}

// rerun reruns a completed workflow from its activity while no worker is
// connected. The client returns as soon as the fallback host accepts the
// rerun, rather than waiting for the new instance to change state, which no
// worker can make it do yet. The fallback host does not dispatch the activity
// it cannot run: it is dispatched, and the new instance completes, once a
// worker connects.
type rerun struct {
	workflow *workflow.Workflow
}

func (r *rerun) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t,
		workflow.WithStagedOperations(),
		withSettle(t, 0),
	)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *rerun) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, r.workflow.Registry().AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallActivity("act").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, r.workflow.Registry().AddActivityN("act", func(task.ActivityContext) (any, error) {
		return doneOutput, nil
	}))

	worker := r.workflow.ConnectWorker(t, ctx, r.workflow.Registry())
	mgmt := r.workflow.ManagementClient(t, ctx)

	src, err := mgmt.ScheduleNewWorkflow(ctx, "wf", api.WithInstanceID("fallback-rerun-src"))
	require.NoError(t, err)
	_, err = mgmt.WaitForWorkflowCompletion(ctx, src)
	require.NoError(t, err)

	worker.Disconnect(t)
	r.workflow.WaitForNoConnectedWorkers(t, ctx)

	// Bounds the rerun and the reads of the new instance.
	octx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()

	// The daprd turns into a fallback host in place, so the workflow actor
	// type stays hosted; the retry rides out the disconnect callback.
	var dst api.InstanceID
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var rerr error
		dst, rerr = mgmt.RerunWorkflowFromEvent(octx, src, 0, api.WithRerunNewInstanceID("fallback-rerun-dst"))
		assert.NoError(c, rerr)
	}, time.Second*5, time.Millisecond*50)
	require.Equal(t, api.InstanceID("fallback-rerun-dst"), dst)

	// The new instance carries the source's history up to the rerun event, so
	// it is running, waiting on the activity no worker can run yet.
	meta, err := mgmt.FetchWorkflowMetadata(octx, dst)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_RUNNING, meta.GetRuntimeStatus())

	r.workflow.ConnectWorker(t, ctx, r.workflow.Registry())

	meta, err = mgmt.WaitForWorkflowCompletion(ctx, dst)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.Equal(t, `"`+doneOutput+`"`, meta.GetOutput().GetValue())
}
