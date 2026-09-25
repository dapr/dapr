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

package childnotify

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(terminated))
}

// terminated verifies a child terminated by its parent's recursive
// terminate commits TERMINATED and settles without a retry storm against
// the already terminal parent.
type terminated struct {
	workflow *workflow.Workflow
}

func (r *terminated) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t)
	return []framework.Option{framework.WithProcesses(r.workflow)}
}

func (r *terminated) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	const childID = "terminated-child"
	reg := r.workflow.Registry()
	require.NoError(t, reg.AddWorkflowN("child", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.WaitForSingleEvent("never", time.Hour).Await(nil)
	}))
	require.NoError(t, reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallChildWorkflow("child", task.WithChildWorkflowInstanceID(childID)).Await(nil)
	}))

	cl := r.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "parent")
	require.NoError(t, err)
	wf.WaitForRuntimeStatus(t, ctx, cl, api.InstanceID(childID), api.RUNTIME_STATUS_RUNNING)

	require.NoError(t, cl.TerminateWorkflow(ctx, id, api.WithRecursiveTerminate(true)))
	wf.WaitForRuntimeStatus(t, ctx, cl, id, api.RUNTIME_STATUS_TERMINATED)
	wf.WaitForRuntimeStatus(t, ctx, cl, api.InstanceID(childID), api.RUNTIME_STATUS_TERMINATED)

	// Give a retry loop time to show itself before asserting there is none.
	time.Sleep(time.Second * 2)
	assert.Zero(t, r.workflow.Scheduler().JobKeyCount(t, ctx, "parent-notify"),
		"a terminal parent must not keep the child re-sending")
}
