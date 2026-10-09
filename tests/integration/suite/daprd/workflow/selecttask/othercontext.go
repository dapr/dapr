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

package selecttask

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(othercontext))
}

// othercontext passes a task created by one workflow instance to Select in
// another instance. Select rejects it: a task from a different context can
// never complete from this context's point of view.
type othercontext struct {
	workflow *workflow.Workflow
	stolen   atomic.Pointer[task.Task]
}

func (o *othercontext) Setup(t *testing.T) []framework.Option {
	o.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(o.workflow),
	}
}

func (o *othercontext) Run(t *testing.T, ctx context.Context) {
	o.workflow.WaitUntilRunning(t, ctx)

	o.workflow.Registry().AddWorkflowN("holder", func(ctx *task.WorkflowContext) (any, error) {
		release := ctx.WaitForSingleEvent("release", -1)
		o.stolen.Store(&release)
		return nil, release.Await(nil)
	})
	o.workflow.Registry().AddWorkflowN("thief", func(ctx *task.WorkflowContext) (any, error) {
		stolen := o.stolen.Load()
		if stolen == nil {
			return nil, errors.New("no task to steal")
		}
		return ctx.Select(*stolen)
	})

	cl := o.workflow.BackendClient(t, ctx)
	holderID, err := cl.ScheduleNewWorkflow(ctx, "holder")
	require.NoError(t, err)
	fworkflow.WaitForWorkflowStartedEvent(t, ctx, cl, holderID)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.NotNil(c, o.stolen.Load())
	}, time.Second*10, time.Millisecond*10)

	thiefID, err := cl.ScheduleNewWorkflow(ctx, "thief")
	require.NoError(t, err)
	meta, err := cl.WaitForWorkflowCompletion(ctx, thiefID)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_FAILED", meta.GetRuntimeStatus().String())
	assert.Contains(t, meta.GetFailureDetails().GetErrorMessage(), "belongs to a different WorkflowContext")

	require.NoError(t, cl.RaiseEvent(ctx, holderID, "release", api.WithEventPayload("done")))
	meta, err = cl.WaitForWorkflowCompletion(ctx, holderID)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
}
