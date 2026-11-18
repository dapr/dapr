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
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(continueasnew))
}

// continueasnew: the name survives continue-as-new generations, so the
// final generation is purged by the entry.
type continueasnew struct {
	workflow *workflow.Workflow
}

func (c *continueasnew) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t, workflow.WithDaprdOptions(0, policy(t, `
      anyTerminal: "1h"
      workflows:
        - name: can
          completed: "1s"
`)))
	return []framework.Option{framework.WithProcesses(c.workflow)}
}

func (c *continueasnew) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, c.workflow.Registry().AddWorkflowN("can", func(ctx *task.WorkflowContext) (any, error) {
		var n int
		if err := ctx.GetInput(&n); err != nil {
			return nil, err
		}
		if n < 3 {
			ctx.ContinueAsNew(n + 1)
		}
		return n, nil
	}))
	cl := c.workflow.BackendClient(t, ctx)
	rows := rowCounter(t, ctx, c.workflow)

	id, err := cl.ScheduleNewWorkflow(ctx, "can", api.WithInput(0))
	require.NoError(t, err)
	meta, err := cl.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.Equal(t, "3", meta.GetOutput().GetValue())
	waitPurged(t, rows, string(id), "the entry did not purge the final generation")
}
