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

package occupied

import (
	"context"
	"sync/atomic"
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
	suite.Register(new(subtree))
}

type subtree struct {
	workflow *workflow.Workflow
}

func (s *subtree) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *subtree) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	const parentID = "occupied-subtree"
	const occupantID = parentID + "-taken"
	const grandchildID = occupantID + ":0000"

	var inActivity atomic.Bool
	releaseCh := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-releaseCh:
		default:
			close(releaseCh)
		}
	})

	reg := s.workflow.Registry()

	reg.AddWorkflowN("occupant", func(ctx *task.WorkflowContext) (any, error) {
		ctx.CallChildWorkflow("occupantchild")
		return nil, nil
	})
	reg.AddWorkflowN("occupantchild", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity("block").Await(nil)
	})
	reg.AddActivityN("block", func(actx task.ActivityContext) (any, error) {
		inActivity.Store(true)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-releaseCh:
			return nil, nil
		}
	})
	reg.AddWorkflowN("child", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	})
	reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallChildWorkflow("child",
			task.WithChildWorkflowInstanceID(occupantID),
		).Await(nil)
	})

	client := s.workflow.BackendClient(t, ctx)

	_, err := client.ScheduleNewWorkflow(ctx, "occupant", api.WithInstanceID(occupantID))
	require.NoError(t, err)
	ometa, err := client.WaitForWorkflowCompletion(ctx, occupantID)
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), ometa.GetRuntimeStatus().String())
	require.Eventually(t, inActivity.Load, time.Second*10, time.Millisecond*10)

	_, err = client.ScheduleNewWorkflow(ctx, "parent", api.WithInstanceID(parentID))
	require.NoError(t, err)

	waitCtx, cancel := context.WithTimeout(ctx, time.Second*30)
	t.Cleanup(cancel)
	meta, err := client.WaitForWorkflowCompletion(waitCtx, parentID)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_FAILED.String(), meta.GetRuntimeStatus().String())
	assert.Contains(t, meta.GetFailureDetails().GetErrorMessage(), "cannot recreate workflow with ID '"+occupantID+"'")

	close(releaseCh)
	_, err = client.WaitForWorkflowCompletion(ctx, grandchildID)
	require.NoError(t, err)

	_, err = client.ScheduleNewWorkflow(ctx, "parent", api.WithInstanceID(parentID+"-second"))
	require.NoError(t, err)
	meta, err = client.WaitForWorkflowCompletion(ctx, parentID+"-second")
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), meta.GetRuntimeStatus().String())
}
