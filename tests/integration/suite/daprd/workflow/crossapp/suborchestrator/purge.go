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
	suite.Register(new(purge))
}

// purge covers recursive PurgeWorkflowState across a cross-app
// sub-orchestrator boundary. The sibling of dapr/dapr #9827 in the purge
// path: durabletask-go's recursive purge calls
// be.GetWorkflowRuntimeState/PurgeWorkflowState directly with the child's
// instance ID and no router, so dapr's actor backend looks the child up on
// the local app, fails with "instance not found", and the entire recursive
// purge errors out before the parent is touched.
//
// Expected behaviour: a recursive purge of the parent must drive both the
// parent (in app0) and the cross-app child (in app1) to a not-found state
// and return success.
type purge struct {
	workflow *workflow.Workflow
}

func (p *purge) Setup(t *testing.T) []framework.Option {
	p.workflow = workflow.New(t, workflow.WithDaprds(2))
	return []framework.Option{
		framework.WithProcesses(p.workflow),
	}
}

func (p *purge) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	const childInstanceID = "test-1-child"

	p.workflow.Registry().AddWorkflowN("MainHelloWorkflow", func(wctx *task.WorkflowContext) (any, error) {
		var out string
		err := wctx.CallChildWorkflow("ChildHelloWorkflow",
			task.WithChildWorkflowAppID(p.workflow.DaprN(1).AppID()),
			task.WithChildWorkflowInstanceID(childInstanceID),
		).Await(&out)
		if err != nil {
			return nil, err
		}
		return out, nil
	})

	p.workflow.RegistryN(1).AddWorkflowN("ChildHelloWorkflow", func(wctx *task.WorkflowContext) (any, error) {
		return "child done", nil
	})

	parent := p.workflow.BackendClient(t, ctx)
	child := p.workflow.BackendClientN(t, ctx, 1)

	parentID, err := parent.ScheduleNewWorkflow(ctx, "MainHelloWorkflow", api.WithInstanceID("test-1"))
	require.NoError(t, err)

	parentMeta, err := parent.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, parentMeta.GetRuntimeStatus())

	childMeta, err := child.WaitForWorkflowCompletion(ctx, api.InstanceID(childInstanceID))
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, childMeta.GetRuntimeStatus())

	// Recursive purge issued against the parent's daprd. Bound the call so a
	// regression hangs the test rather than blocking forever.
	purgeCtx, purgeCancel := context.WithTimeout(ctx, time.Second*30)
	defer purgeCancel()
	require.NoError(t, parent.PurgeWorkflowState(purgeCtx, parentID, api.WithRecursivePurge(true)))

	// Both instances must now be gone — the parent on app0 (driven by the
	// local purge) and the cross-app child on app1 (driven by the recursive
	// fan-out, routed to app1's actor).
	_, err = parent.FetchWorkflowMetadata(ctx, parentID)
	assert.ErrorIs(t, err, api.ErrInstanceNotFound)

	_, err = child.FetchWorkflowMetadata(ctx, api.InstanceID(childInstanceID))
	assert.ErrorIs(t, err, api.ErrInstanceNotFound)
}
