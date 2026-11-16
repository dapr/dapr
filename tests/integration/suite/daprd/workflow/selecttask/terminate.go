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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(terminate))
}

// terminate terminates a workflow blocked in Select. The instance ends as
// TERMINATED and the losing timer's scheduler job is removed.
type terminate struct {
	workflow *workflow.Workflow
}

func (r *terminate) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *terminate) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	r.workflow.Registry().AddWorkflowN("terminate", func(ctx *task.WorkflowContext) (any, error) {
		event := ctx.WaitForSingleEvent("x", -1)
		timer := ctx.CreateTimer(time.Second * 60)
		return ctx.Select(event, timer)
	})

	cl := r.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "terminate")
	require.NoError(t, err)

	r.workflow.Scheduler().WaitJobKeyCount(t, ctx, "timer-", func(n int) bool { return n > 0 })

	require.NoError(t, cl.TerminateWorkflow(ctx, id))

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_TERMINATED", meta.GetRuntimeStatus().String())

	r.workflow.Scheduler().WaitJobKeyCount(t, ctx, "timer-", func(n int) bool { return n == 0 })
}
