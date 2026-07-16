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
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(forcepurge))
}

type forcepurge struct {
	workflow *workflow.Workflow
}

func (p *forcepurge) Setup(t *testing.T) []framework.Option {
	p.workflow = workflow.New(t, workflow.WithDaprds(2))
	return []framework.Option{
		framework.WithProcesses(p.workflow),
	}
}

func (p *forcepurge) Run(t *testing.T, ctx context.Context) {
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
		if err := wctx.CreateTimer(time.Hour).Await(nil); err != nil {
			return nil, err
		}
		return "child done", nil
	})

	parent := p.workflow.BackendClient(t, ctx)
	child := p.workflow.BackendClientN(t, ctx, 1)

	parentID, err := parent.ScheduleNewWorkflow(ctx, "MainHelloWorkflow", api.WithInstanceID("test-1"))
	require.NoError(t, err)

	_, err = parent.WaitForWorkflowStart(ctx, parentID)
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var meta *protos.WorkflowMetadata
		meta, err = child.FetchWorkflowMetadata(ctx, api.InstanceID(childInstanceID))
		if !assert.NoError(c, err) {
			return
		}
		assert.Equal(c, api.RUNTIME_STATUS_RUNNING, meta.GetRuntimeStatus())
	}, time.Second*30, time.Millisecond*10)

	require.NoError(t, parent.PurgeWorkflowState(ctx, parentID,
		api.WithRecursivePurge(true),
		api.WithForcePurge(true),
	))

	_, err = parent.FetchWorkflowMetadata(ctx, parentID)
	require.ErrorIs(t, err, api.ErrInstanceNotFound)

	_, err = child.FetchWorkflowMetadata(ctx, api.InstanceID(childInstanceID))
	require.ErrorIs(t, err, api.ErrInstanceNotFound)
}
