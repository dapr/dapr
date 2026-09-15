/*
Copyright 2025 The Dapr Authors
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

package basic

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
	suite.Register(new(colonid))
}

// colonid pins that an activity of an instance whose ID contains the activity
// actor ID separator ("::") reports its result to that instance: the parent
// ID is everything before the last separator, not the first.
type colonid struct {
	workflow *workflow.Workflow
}

func (c *colonid) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *colonid) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	c.workflow.Registry().AddWorkflowN("withactivity", func(wctx *task.WorkflowContext) (any, error) {
		var out string
		if err := wctx.CallActivity("echo", task.WithActivityInput("in")).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	})
	c.workflow.Registry().AddActivityN("echo", func(actx task.ActivityContext) (any, error) {
		var in string
		if err := actx.GetInput(&in); err != nil {
			return nil, err
		}
		return in + "-done", nil
	})
	cl := c.workflow.BackendClient(t, ctx)

	for _, id := range []api.InstanceID{"colon::id", "a::b::c", "trailing::"} {
		_, err := cl.ScheduleNewWorkflow(ctx, "withactivity", api.WithInstanceID(id))
		require.NoError(t, err)
		meta, err := cl.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err, id)
		assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus(), id)
		assert.Equal(t, `"in-done"`, meta.GetOutput().GetValue(), id)
	}
}
