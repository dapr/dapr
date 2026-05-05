/*
Copyright 2026 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://wwb.apache.org/licenses/LICENSE-2.0
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

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(amenderror))
}

type amenderror struct {
	workflow *workflow.Workflow
}

func (d *amenderror) Setup(t *testing.T) []framework.Option {
	d.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(d.workflow),
	}
}

func (d *amenderror) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)

	wfFn := func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.WaitForSingleEvent("Continue", -1).Await(nil)
	}

	d.workflow.Registry().AddVersionedWorkflowN("workflow", "v1", true, wfFn)

	clientCtx, cancelClient := context.WithCancel(ctx)
	defer cancelClient()
	client := d.workflow.BackendClient(t, clientCtx)
	id, scheduleErr := client.ScheduleNewWorkflow(ctx, "workflow")
	require.NoError(t, scheduleErr)

	wf.WaitForWorkflowStartedEvent(t, ctx, client, id)

	cancelClient()
	d.workflow.ResetRegistry(t)
	d.workflow.Registry().AddVersionedWorkflowN("workflow", "v2", true, wfFn)
	clientCtx, cancelClient = context.WithCancel(ctx)
	defer cancelClient()
	client = d.workflow.BackendClient(t, clientCtx)

	require.NoError(t, client.RaiseEvent(ctx, id, "Continue"))

	wf.WaitForRuntimeStatus(t, ctx, client, id, protos.OrchestrationStatus_ORCHESTRATION_STATUS_STALLED)
	lastEvent := wf.GetLastHistoryEventOfType[protos.HistoryEvent_ExecutionStalled](t, ctx, client, id)
	require.NotNil(t, lastEvent)
	require.Equal(t, protos.StalledReason_VERSION_NOT_AVAILABLE, lastEvent.GetExecutionStalled().GetReason())
	require.Equal(t, "Version not available: v1", lastEvent.GetExecutionStalled().GetDescription())

	cancelClient()
	d.workflow.ResetRegistry(t)
	d.workflow.Registry().AddVersionedWorkflowN("workflow", "v1", true, wfFn)
	clientCtx, cancelClient = context.WithCancel(ctx)
	defer cancelClient()
	client = d.workflow.BackendClient(t, clientCtx)
	md, completeErr := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, completeErr)
	require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, md.RuntimeStatus)
}
