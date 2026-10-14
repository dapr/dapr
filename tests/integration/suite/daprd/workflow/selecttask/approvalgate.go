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
	"sync/atomic"
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
	suite.Register(new(approvalgate))
}

// approvalgate loops over Select(approve, reject, reminder timer). Every
// reminder tick runs an activity and arms a new timer while both event waits
// are carried forward. Discarding a losing wait and creating a fresh one
// would leave the oldest waiter stealing the event and hang the loop.
type approvalgate struct {
	workflow  *workflow.Workflow
	reminders atomic.Int64
}

func (a *approvalgate) Setup(t *testing.T) []framework.Option {
	a.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(a.workflow),
	}
}

func (a *approvalgate) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	a.workflow.Registry().AddWorkflowN("approvalgate", func(ctx *task.WorkflowContext) (any, error) {
		approve := ctx.WaitForSingleEvent("approve", -1)
		reject := ctx.WaitForSingleEvent("reject", -1)
		reminder := ctx.CreateTimer(time.Second)

		for {
			winner, err := ctx.Select(approve, reject, reminder)
			if err != nil {
				return nil, err
			}
			var v string
			switch winner {
			case 0:
				if err := approve.Await(&v); err != nil {
					return nil, err
				}
				return "approve:" + v, nil
			case 1:
				if err := reject.Await(&v); err != nil {
					return nil, err
				}
				return "reject:" + v, nil
			default:
				if err := reminder.Await(nil); err != nil {
					return nil, err
				}
				if err := ctx.CallActivity("remind").Await(nil); err != nil {
					return nil, err
				}
				reminder = ctx.CreateTimer(time.Second)
			}
		}
	})
	a.workflow.Registry().AddActivityN("remind", func(task.ActivityContext) (any, error) {
		a.reminders.Add(1)
		return nil, nil
	})

	cl := a.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "approvalgate")
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, a.reminders.Load(), int64(2))
	}, time.Second*20, time.Millisecond*10)

	require.NoError(t, cl.RaiseEvent(ctx, id, "reject", api.WithEventPayload("nope")))

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.JSONEq(t, `"reject:nope"`, meta.GetOutput().GetValue())
	assert.GreaterOrEqual(t, a.reminders.Load(), int64(2))
}
