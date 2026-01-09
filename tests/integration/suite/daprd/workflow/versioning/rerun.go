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

package versioning

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
	suite.Register(new(rerun))
}

type rerun struct {
	workflow *workflow.Workflow
}

func (d *rerun) Setup(t *testing.T) []framework.Option {
	d.workflow = workflow.New(t,
		workflow.WithDaprds(2),
	)

	return []framework.Option{
		framework.WithProcesses(d.workflow),
	}
}

func (d *rerun) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)

	calledV1 := atomic.Bool{}
	calledV2 := atomic.Bool{}
	require.NoError(t, d.workflow.Registry().AddVersionedOrchestratorN("workflow", "v1", true, func(ctx *task.OrchestrationContext) (any, error) {
		if err := ctx.WaitForSingleEvent("Continue", -1).Await(nil); err != nil {
			return nil, err
		}
		calledV1.Store(true)
		return nil, nil
	}))

	client := d.workflow.BackendClient(t, ctx)
	id, err := client.ScheduleNewOrchestration(ctx, "workflow")
	require.NoError(t, err)

	wf.WaitForOrchestratorStartedEvent(t, ctx, client, id)

	require.False(t, calledV1.Load())
	require.False(t, calledV2.Load())

	require.NoError(t, d.workflow.Registry().AddVersionedOrchestratorN("workflow", "v2", true, func(ctx *task.OrchestrationContext) (any, error) {
		if err := ctx.WaitForSingleEvent("Continue", -1).Await(nil); err != nil {
			return nil, err
		}
		calledV2.Store(true)
		return nil, nil
	}))

	require.NoError(t, client.RaiseEvent(ctx, id, "Continue"))
	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)

	require.True(t, calledV1.Load())
	require.False(t, calledV2.Load())

	orchestratorStarted := wf.GetLastHistoryEventOfType[protos.HistoryEvent_OrchestratorStarted](t, ctx, client, id)
	require.NotNil(t, orchestratorStarted.GetOrchestratorStarted().GetVersion())
	require.Equal(t, "v1", orchestratorStarted.GetOrchestratorStarted().GetVersion().GetName())
}
