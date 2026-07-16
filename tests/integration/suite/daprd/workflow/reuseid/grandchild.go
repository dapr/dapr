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

package reuseid

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

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
	suite.Register(new(grandchild))
}

type grandchild struct {
	workflow *workflow.Workflow
}

func (g *grandchild) Setup(t *testing.T) []framework.Option {
	g.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(g.workflow),
	}
}

func (g *grandchild) Run(t *testing.T, ctx context.Context) {
	g.workflow.WaitUntilRunning(t, ctx)

	const parentID = "reuseid-grandchild"
	const childID = parentID + "-child"
	const grandchildID = parentID + "-grandchild"

	var inActivity atomic.Bool
	releaseCh := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-releaseCh:
		default:
			close(releaseCh)
		}
	})

	reg := g.workflow.Registry()

	reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallChildWorkflow("child",
			task.WithChildWorkflowInstanceID(childID),
		).Await(nil)
	})
	reg.AddWorkflowN("child", func(ctx *task.WorkflowContext) (any, error) {
		ctx.CallChildWorkflow("grandchild", task.WithChildWorkflowInstanceID(grandchildID))
		return nil, nil
	})
	reg.AddWorkflowN("grandchild", func(ctx *task.WorkflowContext) (any, error) {
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

	client := g.workflow.BackendClient(t, ctx)

	_, err := client.ScheduleNewWorkflow(ctx, "parent", api.WithInstanceID(parentID))
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)

	require.Eventually(t, inActivity.Load, time.Second*10, time.Millisecond*10)

	_, err = client.ScheduleNewWorkflow(ctx, "parent", api.WithInstanceID(parentID))
	require.Error(t, err)
	assert.Equal(t, codes.AlreadyExists, status.Code(err), err)
	assert.Contains(t, err.Error(), "cannot recreate workflow with ID '"+parentID+"'")
	assert.Contains(t, err.Error(), "child workflow '"+childID+"'")
	assert.Contains(t, err.Error(), "child workflow '"+grandchildID+"'")

	close(releaseCh)
	_, err = client.WaitForWorkflowCompletion(ctx, grandchildID)
	require.NoError(t, err)

	_, err = client.ScheduleNewWorkflow(ctx, "parent", api.WithInstanceID(parentID))
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
}
