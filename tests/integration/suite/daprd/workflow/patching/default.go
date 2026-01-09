/*
Copyright 2025 The Dapr Authors
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

package patching

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(defaultToPatched))
}

type defaultToPatched struct {
	workflow *workflow.Workflow
}

func (d *defaultToPatched) Setup(t *testing.T) []framework.Option {
	d.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(d.workflow),
	}
}

func (d *defaultToPatched) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)

	patchesFound := []bool{}
	require.NoError(t, d.workflow.Registry().AddOrchestratorN("defaultpatched", func(ctx *task.OrchestrationContext) (any, error) {
		patchesFound = append(patchesFound, ctx.IsPatched("patch1"))
		return nil, nil
	}))

	client := d.workflow.BackendClient(t, ctx)
	id, err := client.ScheduleNewOrchestration(ctx, "defaultpatched")
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)

	assert.Equal(t, []bool{true}, patchesFound)

	hist, err := client.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	var orchestratorStarted *protos.OrchestratorStartedEvent
	for _, event := range hist.Events {
		if event.GetOrchestratorStarted() != nil {
			orchestratorStarted = event.GetOrchestratorStarted()
			break
		}
	}
	require.NotNil(t, orchestratorStarted)
	require.NotNil(t, orchestratorStarted.GetVersion())
	require.Equal(t, []string{"patch1"}, orchestratorStarted.GetVersion().GetPatches())
}
