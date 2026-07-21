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

package terminate

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
	suite.Register(new(zombiechilddown))
}

type zombiechilddown struct {
	workflow *workflow.Workflow
}

func (z *zombiechilddown) Setup(t *testing.T) []framework.Option {
	z.workflow = workflow.New(t, workflow.WithDaprds(2))
	return []framework.Option{
		framework.WithProcesses(z.workflow),
	}
}

func (z *zombiechilddown) Run(t *testing.T, ctx context.Context) {
	z.workflow.WaitUntilRunning(t, ctx)

	const childInstanceID = "zombiechilddown-child"

	z.workflow.Registry().AddWorkflowN("Parent", func(wctx *task.WorkflowContext) (any, error) {
		return nil, wctx.CallChildWorkflow("Child",
			task.WithChildWorkflowAppID(z.workflow.DaprN(1).AppID()),
			task.WithChildWorkflowInstanceID(childInstanceID),
		).Await(nil)
	})

	z.workflow.RegistryN(1).AddWorkflowN("Child", func(wctx *task.WorkflowContext) (any, error) {
		return nil, wctx.CreateTimer(time.Hour).Await(nil)
	})

	parent := z.workflow.BackendClient(t, ctx)
	child := z.workflow.BackendClientN(t, ctx, 1)

	parentID, err := parent.ScheduleNewWorkflow(ctx, "Parent", api.WithInstanceID("zombiechilddown-parent"))
	require.NoError(t, err)

	_, err = parent.WaitForWorkflowStart(ctx, parentID)
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, merr := child.FetchWorkflowMetadata(ctx, api.InstanceID(childInstanceID))
		if !assert.NoError(c, merr) {
			return
		}
		assert.Equal(c, api.RUNTIME_STATUS_RUNNING.String(), meta.GetRuntimeStatus().String())
	}, time.Second*30, time.Millisecond*10)

	// Kill the app hosting the child so the recursive ExecutionTerminated
	// cannot be delivered.
	z.workflow.DaprN(1).Kill(t)

	termCtx, termCancel := context.WithTimeout(ctx, time.Second*20)
	t.Cleanup(termCancel)
	require.NoError(t, parent.TerminateWorkflow(termCtx, parentID))

	// The parent must still durably finalize as TERMINATED.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, merr := parent.FetchWorkflowMetadata(ctx, parentID)
		if !assert.NoError(c, merr) {
			return
		}
		assert.Equal(c, api.RUNTIME_STATUS_TERMINATED.String(), meta.GetRuntimeStatus().String())
	}, time.Second*30, time.Millisecond*50)
}
