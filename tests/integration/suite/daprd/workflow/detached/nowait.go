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
	suite.Register(new(nowait))
}

// nowait proves the load-bearing semantic of detached workflows: the caller
// does NOT wait on the spawn, and the spawn's eventual completion does NOT
// mutate the caller's history.
//
// The spawned workflow blocks on an external event the test never sends
// while the parent is running. The parent must still reach COMPLETED. After
// snapshotting the parent's history, the test releases the spawn and waits
// for it to complete, then re-reads the parent's history and asserts the
// event count is unchanged — no completion / failure event flows back.
type nowait struct {
	workflow *workflow.Workflow
}

func (n *nowait) Setup(t *testing.T) []framework.Option {
	n.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(n.workflow),
	}
}

func (n *nowait) Run(t *testing.T, ctx context.Context) {
	n.workflow.WaitUntilRunning(t, ctx)

	const spawnedInstanceID = "spawned-nowait"

	n.workflow.Registry().AddWorkflowN("Caller", func(ctx *task.WorkflowContext) (any, error) {
		_, err := ctx.ScheduleNewWorkflow("HeldSpawn",
			task.WithDetachedWorkflowInstanceID(spawnedInstanceID))
		return nil, err
	})
	n.workflow.Registry().AddWorkflowN("HeldSpawn", func(ctx *task.WorkflowContext) (any, error) {
		// Block until the test releases us.
		return nil, ctx.WaitForSingleEvent("Continue", 60*time.Second).Await(nil)
	})

	client := n.workflow.BackendClient(t, ctx)
	parentID, err := client.ScheduleNewWorkflow(ctx, "Caller")
	require.NoError(t, err)

	// (1) Parent reaches COMPLETED without waiting for the spawn — if the
	// caller were awaiting the detached instance, this would block forever
	// because the spawn is held on WaitForSingleEvent.
	parentMeta, err := client.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
	require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, parentMeta.GetRuntimeStatus())

	// (2) Snapshot parent history at the earliest possible moment after it
	// reaches COMPLETED. The history must already be final here; any later
	// growth would be a flow-back leak from the spawn.
	histBefore, err := client.GetInstanceHistory(ctx, parentID)
	require.NoError(t, err)
	beforeCount := len(histBefore.GetEvents())
	require.NotZero(t, beforeCount, "parent history must already contain its terminal events")

	// (3) The spawn has actually started (and is therefore now blocked on
	// the event) — without this we couldn't tell apart "spawn never started"
	// from "spawn ran to completion before we looked".
	spawnedStarted, err := client.WaitForWorkflowStart(ctx, api.InstanceID(spawnedInstanceID))
	require.NoError(t, err)
	assert.False(t, api.WorkflowMetadataIsComplete(spawnedStarted),
		"spawn must NOT be terminal yet — it should be blocked on the external event")
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_RUNNING, spawnedStarted.GetRuntimeStatus())

	// (4) Release the spawn and wait for it to complete.
	require.NoError(t, client.RaiseEvent(ctx, api.InstanceID(spawnedInstanceID), "Continue"))
	spawnedTerminal, err := client.WaitForWorkflowCompletion(ctx, api.InstanceID(spawnedInstanceID))
	require.NoError(t, err)
	require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, spawnedTerminal.GetRuntimeStatus())

	// (5) Spawn completion does NOT mutate the parent. If completion or
	// failure flowed back, this count would have grown.
	histAfter, err := client.GetInstanceHistory(ctx, parentID)
	require.NoError(t, err)
	assert.Len(t, histAfter.GetEvents(), beforeCount,
		"detached completion must not append events to the caller's history")
}
