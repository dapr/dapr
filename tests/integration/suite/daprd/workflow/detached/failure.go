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
	"errors"
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
	suite.Register(new(failure))
}

// failure asserts that a detached child's failure does not propagate back to
// the caller: parent terminates COMPLETED, child terminates FAILED, and the
// parent's history records no ChildWorkflowInstanceFailed event.
type failure struct {
	workflow *workflow.Workflow
}

func (f *failure) Setup(t *testing.T) []framework.Option {
	f.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(f.workflow),
	}
}

func (f *failure) Run(t *testing.T, ctx context.Context) {
	f.workflow.WaitUntilRunning(t, ctx)

	const spawnedInstanceID = "spawned-failing"

	f.workflow.Registry().AddWorkflowN("Caller", func(ctx *task.WorkflowContext) (any, error) {
		_, err := ctx.ScheduleNewWorkflow("FailingSpawned",
			task.WithDetachedWorkflowInstanceID(spawnedInstanceID))
		if err != nil {
			return nil, err
		}
		return "caller-done", nil
	})
	f.workflow.Registry().AddWorkflowN("FailingSpawned", func(ctx *task.WorkflowContext) (any, error) {
		return nil, errors.New("spawned failure should NOT propagate")
	})

	client := f.workflow.BackendClient(t, ctx)
	parentID, err := client.ScheduleNewWorkflow(ctx, "Caller")
	require.NoError(t, err)

	parentMeta, err := client.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
	require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, parentMeta.GetRuntimeStatus(),
		"parent must complete independently of the detached child's outcome")
	require.Nil(t, parentMeta.GetFailureDetails(),
		"detached child's failure must not surface as parent failure")

	spawnedMeta, err := client.WaitForWorkflowCompletion(ctx, api.InstanceID(spawnedInstanceID))
	require.NoError(t, err)
	require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED, spawnedMeta.GetRuntimeStatus())

	hist, err := client.GetInstanceHistory(ctx, parentID)
	require.NoError(t, err)
	for _, e := range hist.GetEvents() {
		assert.Nil(t, e.GetChildWorkflowInstanceFailed(),
			"parent history must contain no ChildWorkflowInstanceFailed")
		assert.Nil(t, e.GetChildWorkflowInstanceCompleted(),
			"parent history must contain no ChildWorkflowInstanceCompleted")
	}
}
