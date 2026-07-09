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

package payloadstore

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(childworkflow))
}

// childworkflow asserts that a large child-workflow input is offloaded on
// both sides of the parent-child boundary: in the parent's persisted
// ChildWorkflowInstanceCreated event and in the child's own persisted
// ExecutionStarted event, and that the child's large result is offloaded
// in the parent's persisted ChildWorkflowInstanceCompleted event.
type childworkflow struct {
	workflow *workflow.Workflow
}

func (c *childworkflow) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t, workflow.WithDaprdOptions(0, daprd.WithWorkflowPayloadStoreThreshold(t, 512)))

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *childworkflow) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	childInput := "child-input-marker-" + strings.Repeat("c", 4096)
	childResult := "child-result-marker-" + strings.Repeat("r", 4096)
	const childID = "child-instance"

	c.workflow.Registry().AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		// The child result is offloaded before it reaches this code, so
		// it must not be unmarshaled here.
		if err := ctx.CallChildWorkflow("child",
			task.WithChildWorkflowInput(childInput),
			task.WithChildWorkflowInstanceID(childID),
		).Await(nil); err != nil {
			return nil, err
		}
		return nil, nil
	})
	c.workflow.Registry().AddWorkflowN("child", func(*task.WorkflowContext) (any, error) {
		// The input is offloaded and must not be unmarshaled here; the
		// result is produced in-workflow.
		return childResult, nil
	})

	client := c.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "parent")
	require.NoError(t, err)
	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())

	parentEvents := fworkflow.ReadHistoryEvents(t, ctx, c.workflow.DB(), string(id))
	fworkflow.RequireOffloadedPayload(t, fworkflow.FindPayload(t, parentEvents, fworkflow.ChildWorkflowCreatedPayload), childInput)
	fworkflow.RequireOffloadedPayload(t, fworkflow.FindPayload(t, parentEvents, fworkflow.ChildWorkflowCompletedPayload), childResult)

	childEvents := fworkflow.ReadHistoryEvents(t, ctx, c.workflow.DB(), childID)
	fworkflow.RequireOffloadedPayload(t, fworkflow.FindPayload(t, childEvents, fworkflow.ExecutionStartedPayload), childInput)
	fworkflow.RequireOffloadedPayload(t, fworkflow.FindPayload(t, childEvents, fworkflow.ExecutionCompletedPayload), childResult)

	fworkflow.RequireMarkersAbsent(t, ctx, c.workflow.DB(), "child-input-marker-", "child-result-marker-")
}
