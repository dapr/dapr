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
	suite.Register(new(continueasnew))
}

// continueasnew asserts that a workflow can spawn a detached instance in a
// generation that follows a ContinueAsNew. The first generation calls
// ContinueAsNew without spawning; the second generation spawns a detached
// workflow and returns. The parent must complete COMPLETED, the spawned
// instance must run to completion, and replay across the CAN boundary must
// not cause the spawn to fire more than once.
//
// Note: spawning a detached workflow in the same turn as ContinueAsNew is
// not exercised here on purpose — the durabletask-go applier short-circuits
// on the CAN action and discards any other actions emitted in the same
// turn (same as it does for activities and child workflows), so the
// conventional pattern is "one generation does the work, the next does the
// continuation". This test verifies that pattern works end-to-end.
type continueasnew struct {
	workflow *workflow.Workflow
}

func (c *continueasnew) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *continueasnew) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	const spawnedInstanceID = "spawned-can"

	var callerRuns, spawnedRuns atomic.Int64

	c.workflow.Registry().AddWorkflowN("Caller", func(ctx *task.WorkflowContext) (any, error) {
		callerRuns.Add(1)
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		switch input {
		case "first":
			// First generation: do the continuation work, then CAN. No
			// other actions in this turn — see comment on the test struct.
			ctx.ContinueAsNew("second")
			return nil, nil
		case "second":
			// Second generation: spawn the detached workflow, then return.
			_, err := ctx.ScheduleNewWorkflow("Spawned",
				task.WithDetachedWorkflowInstanceID(spawnedInstanceID),
				task.WithDetachedWorkflowInput("payload"),
			)
			return "done", err
		default:
			return nil, assertNeverInput(input)
		}
	})
	c.workflow.Registry().AddWorkflowN("Spawned", func(ctx *task.WorkflowContext) (any, error) {
		spawnedRuns.Add(1)
		var in string
		if err := ctx.GetInput(&in); err != nil {
			return "", err
		}
		return "spawned:" + in, nil
	})

	client := c.workflow.BackendClient(t, ctx)
	parentID, err := client.ScheduleNewWorkflow(ctx, "Caller", api.WithInput("first"))
	require.NoError(t, err)

	parentMeta, err := client.WaitForWorkflowCompletion(ctx, parentID, api.WithFetchPayloads(true))
	require.NoError(t, err)
	require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, parentMeta.GetRuntimeStatus())
	assert.Equal(t, `"done"`, parentMeta.GetOutput().GetValue(),
		"parent's final output must come from the second generation")

	spawnedMeta, err := client.WaitForWorkflowCompletion(ctx, api.InstanceID(spawnedInstanceID), api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, spawnedMeta.GetRuntimeStatus())
	assert.Equal(t, `"spawned:payload"`, spawnedMeta.GetOutput().GetValue())

	// The spawned body executed exactly once. If the post-CAN replay re-fired
	// the detached action against history we'd see more than 1.
	assert.EqualValues(t, 1, spawnedRuns.Load(),
		"spawned body must execute exactly once across the parent's CAN boundary")

	// Parent's history reflects only the second generation (CAN wiped the
	// first generation's history) and contains exactly one
	// DetachedWorkflowInstanceCreated for the gen-2 spawn.
	hist, err := client.GetInstanceHistory(ctx, parentID)
	require.NoError(t, err)
	var detachedCount int
	var spawnedFromHistory string
	for _, e := range hist.GetEvents() {
		if dw := e.GetDetachedWorkflowInstanceCreated(); dw != nil {
			detachedCount++
			spawnedFromHistory = dw.GetInstanceId()
		}
	}
	assert.Equal(t, 1, detachedCount,
		"parent history must contain exactly one DetachedWorkflowInstanceCreated after CAN")
	assert.Equal(t, spawnedInstanceID, spawnedFromHistory)
}

// assertNeverInput surfaces a clear error when the caller is invoked with
// an unexpected input — guards against silent regressions in generation
// dispatch.
func assertNeverInput(got string) error {
	return assertNeverInputError{got: got}
}

type assertNeverInputError struct{ got string }

func (e assertNeverInputError) Error() string {
	return "Caller received unexpected input: " + e.got
}
