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
	suite.Register(new(crossapp))
}

// crossapp verifies a child in another app delivers its completion after
// its commit and that a re-send across the app boundary is dropped as a
// duplicate by the parent.
type crossapp struct {
	workflow *workflow.Workflow
}

func (x *crossapp) Setup(t *testing.T) []framework.Option {
	x.workflow = workflow.New(t, workflow.WithDaprds(2))
	return []framework.Option{framework.WithProcesses(x.workflow)}
}

func (x *crossapp) Run(t *testing.T, ctx context.Context) {
	x.workflow.WaitUntilRunning(t, ctx)

	const childID = "crossapp-child"
	childApp := x.workflow.DaprN(1).AppID()
	require.NoError(t, x.workflow.RegistryN(1).AddWorkflowN("child", func(*task.WorkflowContext) (any, error) {
		return "remote", nil
	}))
	require.NoError(t, x.workflow.Registry().AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallChildWorkflow("child",
			task.WithChildWorkflowAppID(childApp),
			task.WithChildWorkflowInstanceID(childID),
		).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))

	parent := x.workflow.BackendClient(t, ctx)
	child := x.workflow.BackendClientN(t, ctx, 1)
	id, err := parent.ScheduleNewWorkflow(ctx, "parent")
	require.NoError(t, err)
	meta, err := parent.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.JSONEq(t, `"remote"`, meta.GetOutput().GetValue())
	wf.WaitForRuntimeStatus(t, ctx, child, api.InstanceID(childID), api.RUNTIME_STATUS_COMPLETED)

	x.workflow.StrayFire(t, ctx, 1, childID, false)
	completed, _ := wf.ChildCompletions(t, ctx, parent, id, 0)
	assert.Equal(t, 1, completed, "the cross-app re-send must be dropped as a duplicate")
}
