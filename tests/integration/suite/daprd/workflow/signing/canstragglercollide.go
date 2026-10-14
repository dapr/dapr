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

package signing

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
	suite.Register(new(canstragglercollide))
}

// canstragglercollide is the reused-task-id variant of canstraggler: task ids
// restart at 0 after ContinueAsNew, so generation 1 schedules a real child at
// the same task id the generation-0 straggler claims. The straggler's
// completion arrives while the real child is running and must be classified
// as a stale completion of a superseded invocation and dropped, not as
// history tampering; the parent must complete with the real child's result,
// and the straggler child itself must complete once its dropped completion is
// acknowledged as delivered.
type canstragglercollide struct {
	workflow *workflow.Workflow
}

func (c *canstragglercollide) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t, workflow.WithHistorySigning(t))

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *canstragglercollide) Run(t *testing.T, ctx context.Context) {
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

	// Gen 0: task 0 is a timer, task 1 the fire-and-forget straggler child.
	// Gen 1: task 0 is a short activity, task 1 the awaited real child, so
	// the straggler's task id 1 is reused by a different invocation. The
	// straggler (4s) completes after the real child is scheduled (~0.7s) and
	// before it completes (8s), so its completion lands mid-run against the
	// reused id.
	require.NoError(t, r.AddWorkflowN("loop", func(octx *task.WorkflowContext) (any, error) {
		var gen int
		if err := octx.GetInput(&gen); err != nil {
			return nil, err
		}

		if gen == 0 {
			timer := octx.CreateTimer(100 * time.Millisecond)
			octx.CallChildWorkflow("agent",
				task.WithChildWorkflowInput(4000),
				task.WithChildWorkflowInstanceID("sign-collide-straggler"),
			)
			if err := timer.Await(nil); err != nil {
				return nil, err
			}
			octx.ContinueAsNew(1)
			return nil, nil
		}

		var out int
		if err := octx.CallActivity("think", task.WithActivityInput(300)).Await(&out); err != nil {
			return nil, err
		}
		if err := octx.CallChildWorkflow("agent",
			task.WithChildWorkflowInput(8000),
			task.WithChildWorkflowInstanceID("sign-collide-real"),
		).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))

	client := c.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "loop",
		api.WithInstanceID("sign-collide-parent"),
		api.WithInput(0),
	)
	require.NoError(t, err)

	waitCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	t.Cleanup(cancel)
	meta, err := client.WaitForWorkflowCompletion(waitCtx, id)
	require.NoError(t, err, "parent did not complete: the straggler completion on the reused task id was treated as tampering")
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), meta.GetRuntimeStatus().String(),
		"the stale straggler completion must be dropped, not tombstone the parent; failure details: %v", meta.GetFailureDetails())
	require.Equal(t, "8000", meta.GetOutput().GetValue(),
		"the parent must absorb the real child's result, not the straggler's")

	// The parent's drop is terminal for the sender: the straggler child's
	// completion dispatch must be acknowledged so its own turn commits and it
	// reaches COMPLETED instead of redelivering forever.
	stragglerCtx, stragglerCancel := context.WithTimeout(ctx, 20*time.Second)
	t.Cleanup(stragglerCancel)
	smeta, err := client.WaitForWorkflowCompletion(stragglerCtx, api.InstanceID("sign-collide-straggler"))
	require.NoError(t, err, "straggler child did not complete: its dropped completion was not acknowledged as delivered")
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), smeta.GetRuntimeStatus().String())
}
