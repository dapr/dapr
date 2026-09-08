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

package stalled

import (
	"context"
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
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(recoverclient))
}

// recoverclient verifies a stalled workflow resumes and completes once a worker
// with the required version reconnects, without restarting daprd.
type recoverclient struct {
	workflow *workflow.Workflow
}

func (r *recoverclient) Setup(t *testing.T) []framework.Option {
	// Under WorkflowsFastPath the post-recovery re-drive falls to the janitor
	// backstop; shorten its period so recovery lands inside the assertion
	// windows. Unused in default mode.
	r.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_WORKFLOW_JANITOR_PERIOD", "2s",
		))),
	)
	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *recoverclient) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	r.workflow.Registry().AddVersionedWorkflowN("workflow", "v1", true, func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.WaitForSingleEvent("Continue", -1).Await(nil); err != nil {
			return nil, err
		}
		return nil, nil
	})

	clientCtx, cancelClient := context.WithCancel(ctx)
	client := r.workflow.BackendClient(t, clientCtx)
	id, err := client.ScheduleNewWorkflow(ctx, "workflow")
	require.NoError(t, err)

	wf.WaitForWorkflowStartedEvent(t, ctx, client, id)

	r.workflow.ResetRegistry(t)
	cancelClient()
	r.workflow.WaitForNoConnectedWorkers(t, ctx)

	r.workflow.Registry().AddVersionedWorkflowN("workflow", "v2", true, func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.WaitForSingleEvent("Continue", -1).Await(nil); err != nil {
			return nil, err
		}
		return nil, nil
	})
	clientCtx, cancelClient = context.WithCancel(ctx)
	client = r.workflow.BackendClient(t, clientCtx)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.NoError(c, client.RaiseEvent(ctx, id, "Continue"))
	}, time.Second*20, time.Millisecond*10)

	wf.WaitForRuntimeStatus(t, ctx, client, id, protos.OrchestrationStatus_ORCHESTRATION_STATUS_STALLED)
	lastEvent := wf.GetLastHistoryEventOfType[protos.HistoryEvent_ExecutionStalled](t, ctx, client, id)
	require.NotNil(t, lastEvent)
	require.Equal(t, protos.StalledReason_VERSION_NOT_AVAILABLE, lastEvent.GetExecutionStalled().GetReason())

	r.workflow.ResetRegistry(t)
	cancelClient()
	r.workflow.WaitForNoConnectedWorkers(t, ctx)

	r.workflow.Registry().AddVersionedWorkflowN("workflow", "v1", true, func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.WaitForSingleEvent("Continue", -1).Await(nil); err != nil {
			return nil, err
		}
		return nil, nil
	})
	clientCtx, cancelClient = context.WithCancel(ctx)
	t.Cleanup(cancelClient)
	client = r.workflow.BackendClient(t, clientCtx)

	wf.WaitForRuntimeStatus(t, ctx, client, id, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED)
}
