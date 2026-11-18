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
	suite.Register(new(crossapp))
}

// crossapp: a child scheduled into another app is governed by that app's
// entry for its name, not by the scheduling app's.
type crossapp struct {
	workflow *workflow.Workflow
}

func (c *crossapp) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, policy(t, `
      anyTerminal: "1h"
      workflows:
        - name: child
          completed: "1h"
`)),
		workflow.WithDaprdOptions(1, policy(t, `
      anyTerminal: "1h"
      workflows:
        - name: child
          completed: "1s"
`)),
	)
	return []framework.Option{framework.WithProcesses(c.workflow)}
}

func (c *crossapp) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	const childID = "xapp-child"
	require.NoError(t, c.workflow.Registry().AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallChildWorkflow("child",
			task.WithChildWorkflowAppID(c.workflow.DaprN(1).AppID()),
			task.WithChildWorkflowInstanceID(childID),
		).Await(nil)
	}))
	require.NoError(t, c.workflow.RegistryN(1).AddWorkflowN("child", func(*task.WorkflowContext) (any, error) {
		return nil, nil
	}))

	parentCl := c.workflow.BackendClient(t, ctx)
	childCl := c.workflow.BackendClientN(t, ctx, 1)

	parent, err := parentCl.ScheduleNewWorkflow(ctx, "parent")
	require.NoError(t, err)
	_, err = parentCl.WaitForWorkflowCompletion(ctx, parent)
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		_, ferr := childCl.FetchWorkflowMetadata(ctx, childID)
		assert.ErrorIs(c, ferr, api.ErrInstanceNotFound)
	}, time.Second*20, time.Millisecond*10, "the child app's entry did not purge the child")

	_, err = parentCl.FetchWorkflowMetadata(ctx, parent)
	require.NoError(t, err, "the parent keeps its own app's 1h TTL")
}
