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
	"fmt"
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
	suite.Register(new(childworkflow))
}

// childworkflow Selects over a child workflow that blocks on its own external
// event and a long timer. Releasing the child makes it win, and its output is
// returned by the parent.
type childworkflow struct {
	workflow *workflow.Workflow
}

func (c *childworkflow) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *childworkflow) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	const childID = "select-child"
	c.workflow.Registry().AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		child := ctx.CallChildWorkflow("child", task.WithChildWorkflowInstanceID(childID))
		timer := ctx.CreateTimer(time.Second * 60)

		winner, err := ctx.Select(child, timer)
		if err != nil {
			return nil, err
		}
		if winner != 0 {
			return nil, fmt.Errorf("expected the child to win, got index %d", winner)
		}
		var out string
		if err := child.Await(&out); err != nil {
			return nil, err
		}
		return "parent:" + out, nil
	})
	c.workflow.Registry().AddWorkflowN("child", func(ctx *task.WorkflowContext) (any, error) {
		var v string
		if err := ctx.WaitForSingleEvent("go", -1).Await(&v); err != nil {
			return nil, err
		}
		return "child:" + v, nil
	})

	cl := c.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "parent")
	require.NoError(t, err)
	fworkflow.WaitForWorkflowStartedEvent(t, ctx, cl, api.InstanceID(childID))

	require.NoError(t, cl.RaiseEvent(ctx, api.InstanceID(childID), "go", api.WithEventPayload("now")))

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.JSONEq(t, `"parent:child:now"`, meta.GetOutput().GetValue())
}
