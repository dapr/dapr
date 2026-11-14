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
package perworkflow

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(child))
}

// child: a child workflow is retained by its own name, and purging either
// side of a parent/child pair leaves the other in place.
type child struct {
	workflow *workflow.Workflow
}

func (c *child) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t, workflow.WithDaprdOptions(0, policy(t, `
      anyTerminal: "1h"
      workflows:
        - name: quick
          completed: "1s"
        - name: parentfast
          completed: "1s"
`)))
	return []framework.Option{framework.WithProcesses(c.workflow)}
}

func (c *child) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	const childA, childB = "child-a", "child-b"
	reg := c.workflow.Registry()
	for _, name := range []string{"quick", "keep"} {
		require.NoError(t, reg.AddWorkflowN(name, func(*task.WorkflowContext) (any, error) {
			return nil, nil
		}))
	}
	require.NoError(t, reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallChildWorkflow("quick", task.WithChildWorkflowInstanceID(childA)).Await(nil)
	}))
	require.NoError(t, reg.AddWorkflowN("parentfast", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallChildWorkflow("keep", task.WithChildWorkflowInstanceID(childB)).Await(nil)
	}))
	cl := c.workflow.BackendClient(t, ctx)
	rows := rowCounter(t, ctx, c.workflow)

	parent, err := cl.ScheduleNewWorkflow(ctx, "parent")
	require.NoError(t, err)
	_, err = cl.WaitForWorkflowCompletion(ctx, parent)
	require.NoError(t, err)
	waitPurged(t, rows, childA, "the child's own entry did not purge it")
	assert.Positive(t, rows(t, string(parent)), "the parent has no entry and keeps the app-wide TTL")

	parentFast, err := cl.ScheduleNewWorkflow(ctx, "parentfast")
	require.NoError(t, err)
	_, err = cl.WaitForWorkflowCompletion(ctx, parentFast)
	require.NoError(t, err)
	waitPurged(t, rows, string(parentFast), "the parent's entry did not purge it")
	assert.Positive(t, rows(t, childB), "purging a parent must not cascade to its child")
}
