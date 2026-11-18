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
	suite.Register(new(rerun))
}

// rerun: an instance created by rerun-from-event carries the workflow name,
// so the entry keeps it while the app-wide policy purges other names.
type rerun struct {
	workflow *workflow.Workflow
}

func (r *rerun) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t, workflow.WithDaprdOptions(0, policy(t, `
      completed: "1s"
      workflows:
        - name: keep
          anyTerminal: "1h"
`)))
	return []framework.Option{framework.WithProcesses(r.workflow)}
}

func (r *rerun) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	reg := r.workflow.Registry()
	require.NoError(t, reg.AddWorkflowN("keep", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity("bar").Await(nil)
	}))
	require.NoError(t, reg.AddActivityN("bar", func(task.ActivityContext) (any, error) {
		return nil, nil
	}))
	require.NoError(t, reg.AddWorkflowN("other", func(*task.WorkflowContext) (any, error) {
		return nil, nil
	}))
	cl := r.workflow.BackendClient(t, ctx)
	rows := rowCounter(t, ctx, r.workflow)

	orig, err := cl.ScheduleNewWorkflow(ctx, "keep", api.WithInstanceID("orig"))
	require.NoError(t, err)
	_, err = cl.WaitForWorkflowCompletion(ctx, orig)
	require.NoError(t, err)

	rerunID, err := cl.RerunWorkflowFromEvent(ctx, orig, 0)
	require.NoError(t, err)
	_, err = cl.WaitForWorkflowCompletion(ctx, rerunID)
	require.NoError(t, err)

	// The control proves the app-wide 1s policy is live before the rerun
	// is checked.
	ctrl, err := cl.ScheduleNewWorkflow(ctx, "other")
	require.NoError(t, err)
	_, err = cl.WaitForWorkflowCompletion(ctx, ctrl)
	require.NoError(t, err)
	waitPurged(t, rows, string(ctrl), "the app-wide completed TTL did not purge the control")

	assert.Positive(t, rows(t, string(orig)), "the original must keep its entry's 1h TTL")
	assert.Positive(t, rows(t, string(rerunID)), "the rerun must keep its entry's 1h TTL")
}
