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

package detached

import (
	"context"
	"testing"

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

// purge asserts that purging the parent does NOT touch the detached spawn.
// recursive purge enumerates only ChildWorkflowInstanceCreated events, so
// detached spawns must be left strictly alone.
type purge struct {
	workflow *workflow.Workflow
}

func (p *purge) Setup(t *testing.T) []framework.Option {
	p.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(p.workflow),
	}
}

func (p *purge) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	const spawnedInstanceID = "spawned-purge"

	p.workflow.Registry().AddWorkflowN("Caller", func(ctx *task.WorkflowContext) (any, error) {
		_, err := ctx.ScheduleNewWorkflow("Spawned",
			task.WithDetachedWorkflowInstanceID(spawnedInstanceID))
		return nil, err
	})
	p.workflow.Registry().AddWorkflowN("Spawned", func(ctx *task.WorkflowContext) (any, error) {
		return "spawned-output", nil
	})

	client := p.workflow.BackendClient(t, ctx)
	parentID, err := client.ScheduleNewWorkflow(ctx, "Caller")
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, api.InstanceID(spawnedInstanceID))
	require.NoError(t, err)

	// Recursively purge the parent. The spawned instance must remain.
	require.NoError(t, client.PurgeWorkflowState(ctx, parentID, api.WithRecursivePurge(true)))

	// Parent is gone.
	_, err = client.FetchWorkflowMetadata(ctx, parentID)
	require.Error(t, err)
	assert.ErrorIs(t, err, api.ErrInstanceNotFound)

	// Spawned still reachable with its terminal state.
	spawnedMeta, err := client.FetchWorkflowMetadata(ctx, api.InstanceID(spawnedInstanceID),
		api.WithFetchPayloads(true))
	require.NoError(t, err, "detached spawn must survive recursive purge of the parent")
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, spawnedMeta.GetRuntimeStatus())
}
