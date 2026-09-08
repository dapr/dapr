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
	"time"

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
	suite.Register(new(strayfire))
}

// strayfire verifies a re-sent completion is a no-op for the parent: a
// duplicate in the same generation, and a straggler after the parent
// continued as new and scheduled a different child under the same task id.
type strayfire struct {
	workflow *workflow.Workflow
}

func (s *strayfire) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t)
	return []framework.Option{framework.WithProcesses(s.workflow)}
}

func (s *strayfire) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	reg := s.workflow.Registry()
	require.NoError(t, reg.AddWorkflowN("quick", func(*task.WorkflowContext) (any, error) {
		return "one", nil
	}))
	require.NoError(t, reg.AddWorkflowN("waiting", func(ctx *task.WorkflowContext) (any, error) {
		return "two", ctx.WaitForSingleEvent("go", time.Hour).Await(nil)
	}))
	// Generation 0 completes child c1 (task 0) then continues as new;
	// generation 1 waits on child c2, again task 0.
	require.NoError(t, reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		var gen int
		if err := ctx.GetInput(&gen); err != nil {
			return nil, err
		}
		if gen == 0 {
			if err := ctx.CallChildWorkflow("quick", task.WithChildWorkflowInstanceID("stray-c1")).Await(nil); err != nil {
				return nil, err
			}
			ctx.ContinueAsNew(1)
			return nil, nil
		}
		var out string
		if err := ctx.CallChildWorkflow("waiting", task.WithChildWorkflowInstanceID("stray-c2")).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, reg.AddWorkflowN("single", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallChildWorkflow("quick", task.WithChildWorkflowInstanceID("stray-single")).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	cl := s.workflow.BackendClient(t, ctx)

	t.Run("same generation duplicate", func(t *testing.T) {
		id, err := cl.ScheduleNewWorkflow(ctx, "single")
		require.NoError(t, err)
		_, err = cl.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)

		s.workflow.StrayFire(t, ctx, 0, "stray-single", false)
		completed, _ := wf.ChildCompletions(t, ctx, cl, api.InstanceID(string(id)), 0)
		assert.Equal(t, 1, completed)
	})

	t.Run("straggler after continue as new", func(t *testing.T) {
		id, err := cl.ScheduleNewWorkflow(ctx, "parent", api.WithInput(0))
		require.NoError(t, err)
		wf.WaitForRuntimeStatus(t, ctx, cl, api.InstanceID("stray-c1"), api.RUNTIME_STATUS_COMPLETED)
		wf.WaitForRuntimeStatus(t, ctx, cl, api.InstanceID("stray-c2"), api.RUNTIME_STATUS_RUNNING)

		// c1's completion carries task id 0, which now names c2 in the
		// parent's current generation.
		s.workflow.StrayFire(t, ctx, 0, "stray-c1", false)
		assert.Never(t, func() bool {
			meta, ferr := cl.FetchWorkflowMetadata(ctx, id)
			return ferr == nil && api.WorkflowMetadataIsComplete(meta)
		}, time.Second*2, time.Millisecond*50, "a previous generation's completion must not resolve the current child")

		require.NoError(t, cl.RaiseEvent(ctx, "stray-c2", "go"))
		meta, err := cl.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
		assert.JSONEq(t, `"two"`, meta.GetOutput().GetValue())
		completed, _ := wf.ChildCompletions(t, ctx, cl, api.InstanceID(string(id)), 0)
		assert.Equal(t, 1, completed, "only the current child's completion may be recorded")
	})
}
