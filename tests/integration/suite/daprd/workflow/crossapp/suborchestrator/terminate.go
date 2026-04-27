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
	suite.Register(new(terminate))
}

// A parent workflow in app0 calls a child workflow in app1 (cross-app
// child-workflow).
type terminate struct {
	workflow *workflow.Workflow
}

func (te *terminate) Setup(t *testing.T) []framework.Option {
	te.workflow = workflow.New(t, workflow.WithDaprds(2))
	return []framework.Option{
		framework.WithProcesses(te.workflow),
	}
}

func (te *terminate) Run(t *testing.T, ctx context.Context) {
	te.workflow.WaitUntilRunning(t, ctx)

	const childInstanceID = "test-1-child"

	te.workflow.Registry().AddWorkflowN("MainHelloWorkflow", func(wctx *task.WorkflowContext) (any, error) {
		var out string
		err := wctx.CallChildWorkflow("ChildHelloWorkflow",
			task.WithChildWorkflowAppID(te.workflow.DaprN(1).AppID()),
			task.WithChildWorkflowInstanceID(childInstanceID),
		).Await(&out)
		if err != nil {
			return nil, err
		}
		return out, nil
	})

	te.workflow.RegistryN(1).AddWorkflowN("ChildHelloWorkflow", func(wctx *task.WorkflowContext) (any, error) {
		if err := wctx.CreateTimer(time.Hour).Await(nil); err != nil {
			return nil, err
		}
		return "child done", nil
	})

	parent := te.workflow.BackendClient(t, ctx)
	child := te.workflow.BackendClientN(t, ctx, 1)

	parentID, err := parent.ScheduleNewWorkflow(ctx, "MainHelloWorkflow", api.WithInstanceID("test-1"))
	require.NoError(t, err)

	// Wait for the parent and the cross-app child to both be running before we
	// attempt the terminate.
	_, err = parent.WaitForWorkflowStart(ctx, parentID)
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, err := child.FetchWorkflowMetadata(ctx, api.InstanceID(childInstanceID))
		if !assert.NoError(c, err) {
			return
		}
		assert.Equal(c, api.RUNTIME_STATUS_RUNNING, meta.GetRuntimeStatus())
	}, time.Second*30, time.Millisecond*100)

	require.NoError(t, parent.TerminateWorkflow(ctx, parentID))

	parentMeta, err := parent.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_TERMINATED, parentMeta.GetRuntimeStatus())

	// Terminate is recursive by default, so the cross-app child must also be
	// driven to TERMINATED rather than left running.
	childMeta, err := child.WaitForWorkflowCompletion(ctx, api.InstanceID(childInstanceID))
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_TERMINATED, childMeta.GetRuntimeStatus())
}
