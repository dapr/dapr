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
	suite.Register(new(order))
}

// order holds a start followed by suspends, resumes and an event while no
// worker is connected. The scheduler fires their reminders in no particular
// order, so applying each on its own fire could leave the instance suspended
// or raise the event before the start; they replay in the order requested.
type order struct {
	workflow *workflow.Workflow
}

func (o *order) Setup(t *testing.T) []framework.Option {
	o.workflow = workflow.New(t,
		workflow.WithHeldOperations(),
	)

	return []framework.Option{
		framework.WithProcesses(o.workflow),
	}
}

func (o *order) Run(t *testing.T, ctx context.Context) {
	o.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, o.workflow.Registry().AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.WaitForSingleEvent("go", time.Hour).Await(nil)
	}))

	mgmt := o.workflow.ManagementClient(t, ctx)

	octx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	id, err := mgmt.ScheduleNewWorkflow(octx, "wf", api.WithInstanceID("held-order"))
	require.NoError(t, err)
	const flips = 5
	for range flips {
		require.NoError(t, mgmt.SuspendWorkflow(octx, id, "held"))
		require.NoError(t, mgmt.ResumeWorkflow(octx, id, "held"))
	}
	require.NoError(t, mgmt.RaiseEvent(octx, id, "go"))

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.Len(col, heldKeys(t, ctx, o.workflow, id), 2+2*flips)
	}, time.Second*10, time.Millisecond*10)

	o.workflow.ConnectWorker(t, ctx, o.workflow.Registry())

	wctx, wcancel := context.WithTimeout(ctx, time.Second*30)
	defer wcancel()
	meta, err := mgmt.WaitForWorkflowCompletion(wctx, id)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.Empty(col, heldKeys(t, ctx, o.workflow, id))
	}, time.Second*10, time.Millisecond*10)
}
