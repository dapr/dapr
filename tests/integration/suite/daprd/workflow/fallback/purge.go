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
	suite.Register(new(purge))
}

// purge reads and purges a completed workflow after its worker disconnected:
// a fallback host serves both, where without fallback hosts no daprd hosts the
// workflow actor type and both fail.
type purge struct {
	workflow *workflow.Workflow
}

func (p *purge) Setup(t *testing.T) []framework.Option {
	p.workflow = workflow.New(t,
		workflow.WithStagedOperations(),
		withSettle(t, 0),
	)

	return []framework.Option{
		framework.WithProcesses(p.workflow),
	}
}

func (p *purge) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, p.workflow.Registry().AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		return doneOutput, nil
	}))

	worker := p.workflow.ConnectWorker(t, ctx, p.workflow.Registry())
	mgmt := p.workflow.ManagementClient(t, ctx)

	id, err := mgmt.ScheduleNewWorkflow(ctx, "wf", api.WithInstanceID("fallback-purge"))
	require.NoError(t, err)
	meta, err := mgmt.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())

	worker.Disconnect(t)
	p.workflow.WaitForNoConnectedWorkers(t, ctx)

	octx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()

	// The daprd turns into a fallback host in place, so the workflow actor
	// type stays hosted; the retry rides out the disconnect callback.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, merr := mgmt.FetchWorkflowMetadata(octx, id)
		if assert.NoError(c, merr) {
			assert.Equal(c, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())
			assert.Equal(c, `"done"`, meta.GetOutput().GetValue())
		}
	}, time.Second*5, time.Millisecond*10)

	require.NoError(t, mgmt.PurgeWorkflowState(octx, id))

	_, err = mgmt.FetchWorkflowMetadata(octx, id)
	require.ErrorIs(t, err, api.ErrInstanceNotFound)
}
