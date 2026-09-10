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

package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(childcompletions))
}

type childcompletions struct {
	workflow *workflow.Workflow
}

func (c *childcompletions) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *childcompletions) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	r := c.workflow.Registry()

	require.NoError(t, r.AddActivityN("think", func(actx task.ActivityContext) (any, error) {
		var ms int
		if err := actx.GetInput(&ms); err != nil {
			return nil, err
		}
		time.Sleep(time.Duration(ms) * time.Millisecond)
		return ms, nil
	}))

	require.NoError(t, r.AddWorkflowN("agent", func(octx *task.WorkflowContext) (any, error) {
		var ms int
		if err := octx.GetInput(&ms); err != nil {
			return nil, err
		}
		var out int
		if err := octx.CallActivity("think", task.WithActivityInput(ms)).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))

	require.NoError(t, r.AddWorkflowN("loop", func(octx *task.WorkflowContext) (any, error) {
		var gen int
		if err := octx.GetInput(&gen); err != nil {
			return nil, err
		}

		if gen == 0 {
			timer := octx.CreateTimer(100 * time.Millisecond)
			octx.CallChildWorkflow("agent",
				task.WithChildWorkflowInput(2000),
				task.WithChildWorkflowInstanceID("childcompletions-straggler"),
			)
			if err := timer.Await(nil); err != nil {
				return nil, err
			}
			octx.ContinueAsNew(1)
			return nil, nil
		}

		var out int
		if err := octx.CallActivity("think", task.WithActivityInput(4000)).Await(&out); err != nil {
			return nil, err
		}
		if err := octx.CallChildWorkflow("agent",
			task.WithChildWorkflowInput(200),
			task.WithChildWorkflowInstanceID("childcompletions-real"),
		).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))

	client := c.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "loop",
		api.WithInstanceID("childcompletions-parent"),
		api.WithInput(0),
	)
	require.NoError(t, err)

	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	t.Cleanup(cancel)
	meta, err := client.WaitForWorkflowCompletion(waitCtx, id)
	require.NoError(t, err, "parent did not complete: child workflow completion was lost")
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), meta.GetRuntimeStatus().String(),
		"failure details: %v", meta.GetFailureDetails())
}
