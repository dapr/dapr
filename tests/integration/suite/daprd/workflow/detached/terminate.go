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
	suite.Register(new(terminate))
}

// terminate asserts that recursive termination of the parent does NOT cascade
// into the detached spawn.
type terminate struct {
	workflow *workflow.Workflow
}

func (te *terminate) Setup(t *testing.T) []framework.Option {
	te.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(te.workflow),
	}
}

func (te *terminate) Run(t *testing.T, ctx context.Context) {
	te.workflow.WaitUntilRunning(t, ctx)

	const spawnedInstanceID = "spawned-terminate"

	// Parent yields on an event so it stays running while we observe the
	// spawned outcome under termination.
	te.workflow.Registry().AddWorkflowN("Caller", func(ctx *task.WorkflowContext) (any, error) {
		_, err := ctx.ScheduleNewWorkflow("LongSpawned",
			task.WithDetachedWorkflowInstanceID(spawnedInstanceID))
		if err != nil {
			return nil, err
		}
		return nil, ctx.WaitForSingleEvent("Continue", 60*time.Second).Await(nil)
	})
	te.workflow.Registry().AddWorkflowN("LongSpawned", func(ctx *task.WorkflowContext) (any, error) {
		return "ok", nil
	})

	client := te.workflow.BackendClient(t, ctx)
	parentID, err := client.ScheduleNewWorkflow(ctx, "Caller")
	require.NoError(t, err)

	// Wait for the spawned to complete cleanly before we terminate the parent.
	spawnedMeta, err := client.WaitForWorkflowCompletion(ctx, api.InstanceID(spawnedInstanceID))
	require.NoError(t, err)
	require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, spawnedMeta.GetRuntimeStatus(),
		"spawned must reach COMPLETED before parent termination so we can observe that termination did not change its terminal state")

	require.NoError(t, client.TerminateWorkflow(ctx, parentID, api.WithRecursiveTerminate(true)))
	parentMeta, err := client.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
	require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_TERMINATED, parentMeta.GetRuntimeStatus())

	// Spawned must still be COMPLETED (recursive terminate did not chase it).
	spawnedMetaAfter, err := client.FetchWorkflowMetadata(ctx, api.InstanceID(spawnedInstanceID))
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, spawnedMetaAfter.GetRuntimeStatus(),
		"detached spawn must not be terminated by parent's recursive terminate")
}
