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
	"sync/atomic"
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
	suite.Register(new(defaultid))
}

// defaultid asserts that omitting WithDetachedWorkflowInstanceID produces
// the deterministic suffix "<parentID>-<n>" for each default-ID spawn within
// the same parent execution. Mixing in an explicit-ID spawn must NOT bump
// the default counter. The '-' separator survives dapr's RFC 1123 actor
// reminder job-name validation.
type defaultid struct {
	workflow *workflow.Workflow
	count    atomic.Int64
}

func (d *defaultid) Setup(t *testing.T) []framework.Option {
	d.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(d.workflow),
	}
}

func (d *defaultid) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)

	d.workflow.Registry().AddWorkflowN("Caller", func(ctx *task.WorkflowContext) (any, error) {
		first, err := ctx.ScheduleNewWorkflow("Spawned")
		if err != nil {
			return nil, err
		}
		second, err := ctx.ScheduleNewWorkflow("Spawned")
		if err != nil {
			return nil, err
		}
		// An explicit-ID spawn between defaults must not advance the default counter.
		_, err = ctx.ScheduleNewWorkflow("Spawned",
			task.WithDetachedWorkflowInstanceID(string(ctx.ID)+"-explicit"))
		if err != nil {
			return nil, err
		}
		third, err := ctx.ScheduleNewWorkflow("Spawned")
		if err != nil {
			return nil, err
		}
		return []string{string(first), string(second), string(third)}, nil
	})
	d.workflow.Registry().AddWorkflowN("Spawned", func(ctx *task.WorkflowContext) (any, error) {
		d.count.Add(1)
		return "ok", nil
	})

	client := d.workflow.BackendClient(t, ctx)
	parentID, err := client.ScheduleNewWorkflow(ctx, "Caller")
	require.NoError(t, err)

	parentMeta, err := client.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
	require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, parentMeta.GetRuntimeStatus())

	expected := []api.InstanceID{
		api.InstanceID(string(parentID) + "-0"),
		api.InstanceID(string(parentID) + "-1"),
		api.InstanceID(string(parentID) + "-2"),
	}
	for _, id := range expected {
		spawnedMeta, werr := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, werr, "spawn %s never completed", id)
		require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, spawnedMeta.GetRuntimeStatus())
	}
	explicitMeta, err := client.WaitForWorkflowCompletion(ctx, api.InstanceID(string(parentID)+"-explicit"))
	require.NoError(t, err)
	require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, explicitMeta.GetRuntimeStatus())

	// Exactly four spawns (3 default-ID + 1 explicit). If the orchestrator
	// re-emitted on a replay we would see > 4.
	assert.EqualValues(t, 4, d.count.Load(), "spawned workflow body executed wrong number of times")
}
