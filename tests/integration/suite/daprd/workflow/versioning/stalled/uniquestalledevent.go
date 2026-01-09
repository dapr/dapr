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
	"sync/atomic"
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
	suite.Register(new(uniquestalledevent))
}

type uniquestalledevent struct {
	workflow *workflow.Workflow
}

func (d *uniquestalledevent) Setup(t *testing.T) []framework.Option {
	d.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(d.workflow),
	}
}

func (d *uniquestalledevent) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)

	runv1 := atomic.Bool{}
	runv2 := atomic.Bool{}
	d.workflow.Registry().AddVersionedOrchestratorN("workflow", "v1", true, func(ctx *task.OrchestrationContext) (any, error) {
		if err := ctx.WaitForSingleEvent("Continue", -1).Await(nil); err != nil {
			return nil, err
		}
		runv1.Store(true)
		return nil, nil
	})

	clientCtx, cancelClient := context.WithCancel(ctx)
	defer cancelClient()
	client := d.workflow.BackendClient(t, clientCtx)
	id, err := client.ScheduleNewOrchestration(ctx, "workflow")
	require.NoError(t, err)

	wf.WaitForOrchestratorStartedEvent(t, ctx, client, id)

	d.workflow.ResetRegistry(t)
	cancelClient()

	d.workflow.Registry().AddVersionedOrchestratorN("workflow", "v2", true, func(ctx *task.OrchestrationContext) (any, error) {
		if err := ctx.WaitForSingleEvent("Continue", -1).Await(nil); err != nil {
			return nil, err
		}
		runv2.Store(true)
		return nil, nil
	})
	clientCtx, cancelClient = context.WithCancel(ctx)
	defer cancelClient()
	client = d.workflow.BackendClient(t, clientCtx)

	require.NoError(t, client.RaiseEvent(ctx, id, "Continue"))

	wf.WaitForRuntimeStatus(t, ctx, client, id, protos.OrchestrationStatus_ORCHESTRATION_STATUS_STALLED)

	cancelClient()
	clientCtx, cancelClient = context.WithCancel(ctx)
	defer cancelClient()
	client = d.workflow.BackendClient(t, clientCtx)
	wf.WaitForRuntimeStatus(t, ctx, client, id, protos.OrchestrationStatus_ORCHESTRATION_STATUS_STALLED)

	require.Equal(t, 1, wf.CountHistoryEventsOfType[protos.HistoryEvent_ExecutionStalled](t, ctx, client, id))
}
