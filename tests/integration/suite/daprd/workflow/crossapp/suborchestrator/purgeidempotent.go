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
	suite.Register(new(purgeidempotent))
}

// purgeidempotent verifies recursive PurgeWorkflowState is idempotent across a
// cross-app boundary when some of the parent's children have already been
// purged out-of-band.
type purgeidempotent struct {
	workflow *workflow.Workflow
}

func (p *purgeidempotent) Setup(t *testing.T) []framework.Option {
	p.workflow = workflow.New(t, workflow.WithDaprds(2))
	return []framework.Option{
		framework.WithProcesses(p.workflow),
	}
}

func (p *purgeidempotent) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	childIDs := []string{
		"test-idempotent-child-0",
		"test-idempotent-child-1",
		"test-idempotent-child-2",
		"test-idempotent-child-3",
	}
	prePurged := map[int]bool{0: true, 2: true}

	p.workflow.Registry().AddWorkflowN("MainHelloWorkflow", func(wctx *task.WorkflowContext) (any, error) {
		tasks := make([]task.Task, 0, len(childIDs))
		for _, id := range childIDs {
			tasks = append(tasks, wctx.CallChildWorkflow("ChildHelloWorkflow",
				task.WithChildWorkflowAppID(p.workflow.DaprN(1).AppID()),
				task.WithChildWorkflowInstanceID(id),
			))
		}
		for _, tk := range tasks {
			if err := tk.Await(nil); err != nil {
				return nil, err
			}
		}
		return "parent done", nil
	})

	p.workflow.RegistryN(1).AddWorkflowN("ChildHelloWorkflow", func(wctx *task.WorkflowContext) (any, error) {
		return "idempotent child done", nil
	})

	parent := p.workflow.BackendClient(t, ctx)
	child := p.workflow.BackendClientN(t, ctx, 1)

	parentID, err := parent.ScheduleNewWorkflow(ctx, "MainHelloWorkflow", api.WithInstanceID("test-idempotent"))
	require.NoError(t, err)

	parentMeta, err := parent.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, parentMeta.GetRuntimeStatus())

	for _, id := range childIDs {
		childMeta, cerr := child.WaitForWorkflowCompletion(ctx, api.InstanceID(id))
		require.NoError(t, cerr, "child %s did not complete", id)
		assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, childMeta.GetRuntimeStatus(), "child %s status", id)
	}

	for i, id := range childIDs {
		if !prePurged[i] {
			continue
		}
		require.NoError(t, child.PurgeWorkflowState(ctx, api.InstanceID(id)))
		_, err = child.FetchWorkflowMetadata(ctx, api.InstanceID(id))
		require.ErrorIs(t, err, api.ErrInstanceNotFound)
	}

	for i, id := range childIDs {
		if prePurged[i] {
			continue
		}
		_, err = child.FetchWorkflowMetadata(ctx, api.InstanceID(id))
		require.NoError(t, err)
	}

	require.NoError(t, parent.PurgeWorkflowState(ctx, parentID, api.WithRecursivePurge(true)))

	_, err = parent.FetchWorkflowMetadata(ctx, parentID)
	require.ErrorIs(t, err, api.ErrInstanceNotFound)

	for _, id := range childIDs {
		_, err = child.FetchWorkflowMetadata(ctx, api.InstanceID(id))
		require.ErrorIs(t, err, api.ErrInstanceNotFound,
			"child %s should be purged after recursive purge", id)
	}
}
