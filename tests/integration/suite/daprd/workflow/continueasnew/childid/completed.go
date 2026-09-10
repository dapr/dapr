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

package childid

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
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(completed))
}

type completed struct {
	workflow *workflow.Workflow
}

func (c *completed) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *completed) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	const parentID = "childid-completed"
	const pinnedID = parentID + "-pinned"

	reg := c.workflow.Registry()

	reg.AddWorkflowN("quick", func(ctx *task.WorkflowContext) (any, error) {
		var gen int
		if err := ctx.GetInput(&gen); err != nil {
			return nil, err
		}
		return gen, nil
	})
	reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		var gen int
		if err := ctx.GetInput(&gen); err != nil {
			return nil, err
		}
		var out int
		if err := ctx.CallChildWorkflow("quick",
			task.WithChildWorkflowInstanceID(pinnedID),
			task.WithChildWorkflowInput(gen),
		).Await(&out); err != nil {
			return nil, err
		}
		if gen < 2 {
			ctx.ContinueAsNew(gen + 1)
			return nil, nil
		}
		return out, nil
	})

	client := c.workflow.BackendClient(t, ctx)

	_, err := client.ScheduleNewWorkflow(ctx, "parent",
		api.WithInstanceID(parentID),
		api.WithInput(0),
	)
	require.NoError(t, err)

	waitCtx, cancel := context.WithTimeout(ctx, time.Second*30)
	t.Cleanup(cancel)
	meta, err := client.WaitForWorkflowCompletion(waitCtx, parentID)
	require.NoError(t, err, "reuse of a completed child's instance ID across generations must keep working")
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), meta.GetRuntimeStatus().String())
	assert.Equal(t, "2", meta.GetOutput().GetValue())

	ometa, err := client.FetchWorkflowMetadata(ctx, api.InstanceID(pinnedID))
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), ometa.GetRuntimeStatus().String())
	assert.Equal(t, "quick", ometa.GetName())
}
