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

package held

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
	suite.Register(new(events))
}

// events raises an event at a workflow whose start is itself held. Both are
// held, and the workflow receives the event once a worker connects, whichever
// of the two the scheduler delivers first.
type events struct {
	workflow *workflow.Workflow
}

func (e *events) Setup(t *testing.T) []framework.Option {
	e.workflow = workflow.New(t,
		workflow.WithHeldOperations(),
	)

	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *events) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, e.workflow.Registry().AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		var got string
		if err := ctx.WaitForSingleEvent("go", time.Hour).Await(&got); err != nil {
			return nil, err
		}
		return got, nil
	}))

	mgmt := e.workflow.ManagementClient(t, ctx)
	id, err := mgmt.ScheduleNewWorkflow(ctx, "wf", api.WithInstanceID("held-events"))
	require.NoError(t, err)
	require.NoError(t, mgmt.RaiseEvent(ctx, id, "go", api.WithEventPayload("payload")))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, heldKeys(t, ctx, e.workflow, id), 2)
	}, time.Second*10, time.Millisecond*10)

	e.workflow.ConnectWorker(t, ctx, e.workflow.Registry())

	meta, err := mgmt.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.Equal(t, `"payload"`, meta.GetOutput().GetValue())
}
