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

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(reuseid))
}

// reuseid: re-scheduling an instance ID under a different workflow name
// takes the new name's TTL, replacing the previous run's reminder.
type reuseid struct {
	workflow *workflow.Workflow
}

func (r *reuseid) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t, workflow.WithDaprdOptions(0, policy(t, `
      workflows:
        - name: slow
          completed: "1h"
        - name: fast
          completed: "1s"
`)))
	return []framework.Option{framework.WithProcesses(r.workflow)}
}

func (r *reuseid) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	reg := r.workflow.Registry()
	require.NoError(t, reg.AddWorkflowN("slow", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.WaitForSingleEvent("release", time.Minute).Await(nil)
	}))
	require.NoError(t, reg.AddWorkflowN("fast", func(*task.WorkflowContext) (any, error) {
		return nil, nil
	}))
	cl := r.workflow.BackendClient(t, ctx)
	rows := rowCounter(t, ctx, r.workflow)

	const id = api.InstanceID("reused")
	_, err := cl.ScheduleNewWorkflow(ctx, "slow", api.WithInstanceID(id))
	require.NoError(t, err)
	require.NoError(t, cl.RaiseEvent(ctx, id, "release"))
	_, err = cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	// Run 1 queues its 1h reminder.
	r.workflow.Scheduler().WaitJobKeyCount(t, ctx, "||retention", func(n int) bool { return n == 1 })

	_, err = cl.ScheduleNewWorkflow(ctx, "fast", api.WithInstanceID(id))
	require.NoError(t, err)
	_, err = cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	waitPurged(t, rows, string(id), "run 2's name did not govern the reused ID")
	// Run 1 reminder is replaced by run 2, not left behind.
	r.workflow.Scheduler().WaitJobKeyCount(t, ctx, "||retention", func(n int) bool { return n == 0 })
}
