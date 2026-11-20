/*
Copyright 2025 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://wwb.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package rerun

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(suborchestration))
}

type suborchestration struct {
	workflow *workflow.Workflow
}

func (s *suborchestration) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *suborchestration) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	var act atomic.Int64
	s.workflow.Registry().AddWorkflowN("foo1", func(ctx *task.WorkflowContext) (any, error) {
		require.NoError(t, ctx.CallActivity("bar").Await(nil))
		require.NoError(t, ctx.CallChildWorkflow("foo2").Await(nil))
		require.NoError(t, ctx.CallChildWorkflow("foo3").Await(nil))
		return nil, nil
	})
	s.workflow.Registry().AddWorkflowN("foo2", func(ctx *task.WorkflowContext) (any, error) {
		require.NoError(t, ctx.CallActivity("bar").Await(nil))
		require.NoError(t, ctx.CallChildWorkflow("foo3").Await(nil))
		return nil, nil
	})
	s.workflow.Registry().AddWorkflowN("foo3", func(ctx *task.WorkflowContext) (any, error) {
		require.NoError(t, ctx.CallActivity("bar").Await(nil))
		return nil, nil
	})
	s.workflow.Registry().AddActivityN("bar", func(ctx task.ActivityContext) (any, error) {
		act.Add(1)
		return nil, nil
	})
	client := s.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "foo1", api.WithInstanceID("abc"))
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, int64(4), act.Load())

	act.Store(0)
	newID, err := client.RerunWorkflowFromEvent(ctx, id, 0)
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, newID)
	require.NoError(t, err)
	assert.Equal(t, int64(4), act.Load())

	newID, err = client.RerunWorkflowFromEvent(ctx, id, 1)
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, newID)
	require.NoError(t, err)
	newID, err = client.RerunWorkflowFromEvent(ctx, id, 2)
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, newID)
	require.NoError(t, err)
	_, err = client.RerunWorkflowFromEvent(ctx, id, 3)
	assert.Equal(t, status.Error(codes.NotFound, "target event '*protos.HistoryEvent_ExecutionCompleted' with ID '3' is not an event that can be rerun"), err)

	// We can't rerun an event _inside_ a sub-orchestration because it is a new
	// workflow instance!
	_, err = client.RerunWorkflowFromEvent(ctx, id, 4)
	assert.Equal(t, status.Error(codes.NotFound, "does not have history event with ID '4'"), err)

	// Target children and a grandchild directly. Only the chosen subtree
	// executes; the original parent receives no second completion.
	for _, tc := range []struct {
		id         string
		activities int64
	}{
		{"abc:0001", 2},
		{"abc:0002", 1},
		{"abc:0001:0001", 1},
	} {
		act.Store(0)
		rerunID, rerunErr := client.RerunWorkflowFromEvent(ctx, api.InstanceID(tc.id), 0)
		require.NoError(t, rerunErr)
		meta, waitErr := client.WaitForWorkflowCompletion(ctx, rerunID)
		require.NoError(t, waitErr)
		assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
		assert.Empty(t, meta.GetParentInstanceId())
		assert.Equal(t, tc.activities, act.Load())
	}
}
