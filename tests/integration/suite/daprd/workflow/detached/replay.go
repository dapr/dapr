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
	suite.Register(new(replay))
}

// replay forces the parent through a replay (yield on external event, then
// resume) and asserts the spawned instance is created exactly once with a
// stable ID — the pending action must be retired by the matching
// DetachedWorkflowInstanceCreated event in history rather than re-fired.
type replay struct {
	workflow *workflow.Workflow
	spawned  atomic.Int64
}

func (r *replay) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *replay) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	const spawnedInstanceID = "spawned-replay"

	r.workflow.Registry().AddWorkflowN("Caller", func(ctx *task.WorkflowContext) (any, error) {
		_, err := ctx.ScheduleNewWorkflow("Spawned",
			task.WithDetachedWorkflowInstanceID(spawnedInstanceID))
		if err != nil {
			return nil, err
		}
		// Yield on an external event so the orchestrator function exits
		// and is replayed when the event arrives.
		if err := ctx.WaitForSingleEvent("Continue", 30*time.Second).Await(nil); err != nil {
			return nil, err
		}
		return "ok", nil
	})
	r.workflow.Registry().AddWorkflowN("Spawned", func(ctx *task.WorkflowContext) (any, error) {
		r.spawned.Add(1)
		return "ok", nil
	})

	client := r.workflow.BackendClient(t, ctx)
	parentID, err := client.ScheduleNewWorkflow(ctx, "Caller")
	require.NoError(t, err)

	// Wait for the spawned workflow to complete — proves the spawn message
	// was dispatched on the first orchestrator run, before the replay.
	_, err = client.WaitForWorkflowCompletion(ctx, api.InstanceID(spawnedInstanceID))
	require.NoError(t, err)

	require.NoError(t, client.RaiseEvent(ctx, parentID, "Continue"))

	parentMeta, err := client.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
	require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, parentMeta.GetRuntimeStatus())

	assert.EqualValues(t, 1, r.spawned.Load(),
		"spawned body must execute exactly once even though parent replayed")

	hist, err := client.GetInstanceHistory(ctx, parentID)
	require.NoError(t, err)
	var detachedCount int
	for _, e := range hist.GetEvents() {
		if e.GetDetachedWorkflowInstanceCreated() != nil {
			detachedCount++
		}
	}
	assert.Equal(t, 1, detachedCount,
		"history must record exactly one DetachedWorkflowInstanceCreated event after replay")
}
