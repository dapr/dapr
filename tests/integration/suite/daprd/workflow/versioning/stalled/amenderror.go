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
	"time"

	"github.com/stretchr/testify/assert"
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

func (d *amenderror) registerActivity(registry *task.TaskRegistry) {
	registry.AddActivityN("activity", func(ctx task.ActivityContext) (any, error) {
		return nil, nil
	})
}

func (d *amenderror) Run(t *testing.T, ctx context.Context) {
	d.registerActivity(d.workflow.Registry())
	d.workflow.WaitUntilRunning(t, ctx)

	d.workflow.Registry().AddVersionedOrchestratorN("workflow", "v1", true, func(ctx *task.OrchestrationContext) (any, error) {
		if err := ctx.WaitForSingleEvent("Continue", -1).Await(nil); err != nil {
			return nil, err
		}
		if err := ctx.CallActivity("activity").Await(nil); err != nil {
			return nil, err
		}
		return nil, nil
	})

	clientCtx, cancelClient := context.WithCancel(ctx)
	defer cancelClient()
	client := d.workflow.BackendClient(t, clientCtx)
	id, err := client.ScheduleNewOrchestration(ctx, "workflow")
	require.NoError(t, err)

	wf.WaitForOrchestratorStartedEvent(t, ctx, client, id)

	cancelClient()
	d.workflow.ResetRegistry(t)
	d.registerActivity(d.workflow.Registry())
	d.workflow.Registry().AddVersionedOrchestratorN("workflow", "v2", true, func(ctx *task.OrchestrationContext) (any, error) {
		if err := ctx.WaitForSingleEvent("Continue", -1).Await(nil); err != nil {
			return nil, err
		}
		if err := ctx.CallActivity("activity").Await(nil); err != nil {
			return nil, err
		}
		return nil, nil
	})
	clientCtx, cancelClient = context.WithCancel(ctx)
	defer cancelClient()
	client = d.workflow.BackendClient(t, clientCtx)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.NoError(c, client.RaiseEvent(ctx, id, "Continue"))
	}, time.Second*20, time.Millisecond*10)

	wf.WaitForRuntimeStatus(t, ctx, client, id, protos.OrchestrationStatus_ORCHESTRATION_STATUS_STALLED)
	lastEvent := wf.GetLastHistoryEventOfType[protos.HistoryEvent_ExecutionStalled](t, ctx, client, id)
	require.NotNil(t, lastEvent)
	require.Equal(t, protos.StalledReason_VERSION_NOT_AVAILABLE, lastEvent.GetExecutionStalled().GetReason())
	require.Equal(t, "Version not available: v1", lastEvent.GetExecutionStalled().GetDescription())

	cancelClient()
	d.workflow.ResetRegistry(t)
	d.registerActivity(d.workflow.Registry())
	d.workflow.Registry().AddVersionedOrchestratorN("workflow", "v1", true, func(ctx *task.OrchestrationContext) (any, error) {
		if err := ctx.WaitForSingleEvent("Continue", -1).Await(nil); err != nil {
			return nil, err
		}
		if err := ctx.CallActivity("activity").Await(nil); err != nil {
			return nil, err
		}
		return nil, nil
	})
	clientCtx, cancelClient = context.WithCancel(ctx)
	defer cancelClient()
	client = d.workflow.BackendClient(t, clientCtx)
	wf.WaitForRuntimeStatus(t, ctx, client, id, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED)
}
