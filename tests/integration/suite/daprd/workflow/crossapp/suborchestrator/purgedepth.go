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

package suborchestrator

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
	suite.Register(new(purgedepth))
}

// purgedepth covers recursive PurgeWorkflowState across more than one cross-app
// boundary (A -> B -> C). Each app must hand its subtree off to the next app
// when it encounters a foreign sub-orchestration, so the recursive purge
// reaches every level rather than stopping at the first hop.
type purgedepth struct {
	workflow *workflow.Workflow
}

func (p *purgedepth) Setup(t *testing.T) []framework.Option {
	p.workflow = workflow.New(t, workflow.WithDaprds(3))
	return []framework.Option{
		framework.WithProcesses(p.workflow),
	}
}

func (p *purgedepth) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	const (
		bID = "test-1-b"
		cID = "test-1-c"
	)

	p.workflow.Registry().AddWorkflowN("WorkflowA", func(wctx *task.WorkflowContext) (any, error) {
		var out string
		err := wctx.CallChildWorkflow("WorkflowB",
			task.WithChildWorkflowAppID(p.workflow.DaprN(1).AppID()),
			task.WithChildWorkflowInstanceID(bID),
		).Await(&out)
		if err != nil {
			return nil, err
		}
		return out, nil
	})

	p.workflow.RegistryN(1).AddWorkflowN("WorkflowB", func(wctx *task.WorkflowContext) (any, error) {
		var out string
		err := wctx.CallChildWorkflow("WorkflowC",
			task.WithChildWorkflowAppID(p.workflow.DaprN(2).AppID()),
			task.WithChildWorkflowInstanceID(cID),
		).Await(&out)
		if err != nil {
			return nil, err
		}
		return out, nil
	})

	p.workflow.RegistryN(2).AddWorkflowN("WorkflowC", func(wctx *task.WorkflowContext) (any, error) {
		return "c done", nil
	})

	a := p.workflow.BackendClient(t, ctx)
	b := p.workflow.BackendClientN(t, ctx, 1)
	c := p.workflow.BackendClientN(t, ctx, 2)

	aID, err := a.ScheduleNewWorkflow(ctx, "WorkflowA", api.WithInstanceID("test-1"))
	require.NoError(t, err)

	aMeta, err := a.WaitForWorkflowCompletion(ctx, aID)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, aMeta.GetRuntimeStatus())

	bMeta, err := b.WaitForWorkflowCompletion(ctx, api.InstanceID(bID))
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, bMeta.GetRuntimeStatus())

	cMeta, err := c.WaitForWorkflowCompletion(ctx, api.InstanceID(cID))
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, cMeta.GetRuntimeStatus())

	require.NoError(t, a.PurgeWorkflowState(ctx, aID, api.WithRecursivePurge(true)))

	_, err = a.FetchWorkflowMetadata(ctx, aID)
	require.ErrorIs(t, err, api.ErrInstanceNotFound)

	_, err = b.FetchWorkflowMetadata(ctx, api.InstanceID(bID))
	require.ErrorIs(t, err, api.ErrInstanceNotFound)

	_, err = c.FetchWorkflowMetadata(ctx, api.InstanceID(cID))
	require.ErrorIs(t, err, api.ErrInstanceNotFound)
}
