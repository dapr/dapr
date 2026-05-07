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
	suite.Register(new(single))
}

// single asserts the happy path: a parent spawns one detached workflow with
// an explicit instance ID and full optional fields, the parent completes
// independently of the spawn, the spawned instance runs to completion, and
// the parent's history records exactly one DetachedWorkflowInstanceCreated
// event with no ChildWorkflowInstanceCreated counterpart (which would imply
// recursive purge / terminate could chase the spawn).
type single struct {
	workflow *workflow.Workflow
}

func (s *single) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *single) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	const spawnedInstanceID = "spawned-single"

	s.workflow.Registry().AddWorkflowN("Caller", func(ctx *task.WorkflowContext) (any, error) {
		id, err := ctx.ScheduleNewWorkflow("Spawned",
			task.WithDetachedWorkflowInstanceID(spawnedInstanceID),
			task.WithDetachedWorkflowInput("payload"),
			task.WithDetachedWorkflowTags(map[string]string{"team": "growth"}),
		)
		if err != nil {
			return nil, err
		}
		return string(id), nil
	})
	s.workflow.Registry().AddWorkflowN("Spawned", func(ctx *task.WorkflowContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return "", err
		}
		return "spawned-saw:" + input, nil
	})

	client := s.workflow.BackendClient(t, ctx)
	parentID, err := client.ScheduleNewWorkflow(ctx, "Caller")
	require.NoError(t, err)

	parentMeta, err := client.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
	require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, parentMeta.GetRuntimeStatus())

	spawnedMeta, err := client.WaitForWorkflowCompletion(ctx, api.InstanceID(spawnedInstanceID),
		api.WithFetchPayloads(true))
	require.NoError(t, err)
	require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, spawnedMeta.GetRuntimeStatus())
	assert.Equal(t, `"spawned-saw:payload"`, spawnedMeta.GetOutput().GetValue())

	// Walk the parent's signed history. Both workflows are terminal at this
	// point so the history is frozen — one read is enough. The parent must
	// have exactly one DetachedWorkflowInstanceCreated for the spawn and
	// zero ChildWorkflowInstanceCreated events (which would imply recursive
	// purge / terminate could chase the spawn).
	hist, err := client.GetInstanceHistory(ctx, parentID)
	require.NoError(t, err)
	var detachedCount, childCount int
	var spawnedFromEvent string
	for _, e := range hist.GetEvents() {
		if dw := e.GetDetachedWorkflowInstanceCreated(); dw != nil {
			detachedCount++
			spawnedFromEvent = dw.GetInstanceId()
		}
		if e.GetChildWorkflowInstanceCreated() != nil {
			childCount++
		}
	}
	assert.Equal(t, 1, detachedCount, "exactly one DetachedWorkflowInstanceCreated expected")
	assert.Equal(t, 0, childCount, "detached spawn must not record a ChildWorkflowInstanceCreated event")
	assert.Equal(t, spawnedInstanceID, spawnedFromEvent)
}
