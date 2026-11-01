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
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(foreigntask))
}

// fakeTask is a Task implementation that no WorkflowContext created.
type fakeTask struct{}

func (fakeTask) Await(any) error         { return nil }
func (fakeTask) TaskExecutionId() string { return "" }

// foreigntask passes a Task not created by the SDK to Select. Select rejects
// it with ErrTaskNotSelectable, which the workflow can match with errors.Is.
type foreigntask struct {
	workflow *workflow.Workflow
}

func (f *foreigntask) Setup(t *testing.T) []framework.Option {
	f.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(f.workflow),
	}
}

func (f *foreigntask) Run(t *testing.T, ctx context.Context) {
	f.workflow.WaitUntilRunning(t, ctx)

	f.workflow.Registry().AddWorkflowN("foreigntask", func(ctx *task.WorkflowContext) (any, error) {
		_, err := ctx.Select(fakeTask{})
		if !errors.Is(err, task.ErrTaskNotSelectable) {
			return nil, errors.New("expected ErrTaskNotSelectable")
		}
		return err.Error(), nil
	})

	cl := f.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "foreigntask")
	require.NoError(t, err)

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.Contains(t, meta.GetOutput().GetValue(), "task at index 0: task does not support Select")
}
