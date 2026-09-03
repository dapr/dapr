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
	"errors"
	"testing"

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
	suite.Register(new(failed))
}

// failed verifies a failing child delivers exactly one
// ChildWorkflowInstanceFailed, with its failure details, after committing
// its own FAILED state, and that a stray fire on it does not add another.
type failed struct {
	workflow *workflow.Workflow
}

func (f *failed) Setup(t *testing.T) []framework.Option {
	f.workflow = workflow.New(t)
	return []framework.Option{framework.WithProcesses(f.workflow)}
}

func (f *failed) Run(t *testing.T, ctx context.Context) {
	f.workflow.WaitUntilRunning(t, ctx)

	const childID = "failed-child"
	reg := f.workflow.Registry()
	require.NoError(t, reg.AddWorkflowN("child", func(*task.WorkflowContext) (any, error) {
		return nil, errors.New("boom")
	}))
	require.NoError(t, reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		err := ctx.CallChildWorkflow("child", task.WithChildWorkflowInstanceID(childID)).Await(nil)
		return err != nil, nil
	}))

	cl := f.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "parent")
	require.NoError(t, err)
	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.JSONEq(t, `true`, meta.GetOutput().GetValue(), "the parent must observe the child's failure")
	wf.WaitForRuntimeStatus(t, ctx, cl, api.InstanceID(childID), api.RUNTIME_STATUS_FAILED)

	hist, err := cl.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	var failures int
	for _, e := range hist.GetEvents() {
		if fe := e.GetChildWorkflowInstanceFailed(); fe != nil {
			failures++
			assert.Contains(t, fe.GetFailureDetails().GetErrorMessage(), "boom")
		}
	}
	assert.Equal(t, 1, failures)

	f.workflow.StrayFire(t, ctx, 0, childID, false)
	_, failures = wf.ChildCompletions(t, ctx, cl, api.InstanceID(string(id)), 0)
	assert.Equal(t, 1, failures, "the re-sent failure must be dropped as a duplicate")
}
