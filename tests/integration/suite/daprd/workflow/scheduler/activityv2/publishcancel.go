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

package activityv2

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(publishcancel))
}

// publishcancel: an activity body completes and its result is in hand when the
// invocation context is cut, which is what a placement drain does. Execution is
// at-least-once, so re-running the body is legal; discarding a result the
// runtime already holds is still a defect, because the re-run is avoidable and
// users observe the side effects of every extra execution.
type publishcancel struct {
	workflow *workflow.Workflow
}

func (p *publishcancel) Setup(t *testing.T) []framework.Option {
	p.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithExecOptions(exec.WithEnvVars(t,
			// Cut the context on the first publish only, so the retry that
			// follows is free to succeed.
			"DAPR_WORKFLOW_TEST_CANCEL_BEFORE_PUBLISH", "1",
		))),
	)
	return []framework.Option{framework.WithProcesses(p.workflow)}
}

func (p *publishcancel) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	var executions atomic.Int64
	require.NoError(t, p.workflow.Registry().AddWorkflowN("PublishCancel", func(c *task.WorkflowContext) (any, error) {
		var out string
		if err := c.CallActivity("Once").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, p.workflow.Registry().AddActivityN("Once", func(task.ActivityContext) (any, error) {
		executions.Add(1)
		return "done", nil
	}))

	cl := p.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "PublishCancel")
	require.NoError(t, err)

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.JSONEq(t, `"done"`, meta.GetOutput().GetValue())

	// The cut landed after the body returned, so the result existed and only
	// needed delivering. Nothing here lost it: no host died, no placement
	// moved, the workflow stayed on this daprd. A second execution is
	// therefore avoidable waste, not the at-least-once floor being exercised.
	assert.Equal(t, int64(1), executions.Load(),
		"a result the runtime already holds must be published, not recomputed")
	assert.Never(t, func() bool { return executions.Load() > 1 },
		time.Second*3, time.Millisecond*10)
}
