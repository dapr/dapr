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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(purgedchild))
}

type purgedchild struct {
	workflow *workflow.Workflow
}

func (p *purgedchild) Setup(t *testing.T) []framework.Option {
	p.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(p.workflow),
	}
}

func (p *purgedchild) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	const parentID = "reuseid-purgedchild"
	const childID = parentID + "-child"

	reg := p.workflow.Registry()

	reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallChildWorkflow("child",
			task.WithChildWorkflowInstanceID(childID),
		).Await(nil)
	})
	reg.AddWorkflowN("child", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	})

	client := p.workflow.BackendClient(t, ctx)

	_, err := client.ScheduleNewWorkflow(ctx, "parent", api.WithInstanceID(parentID))
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)

	require.NoError(t, client.PurgeWorkflowState(ctx, api.InstanceID(childID)))

	_, err = client.ScheduleNewWorkflow(ctx, "parent", api.WithInstanceID(parentID))
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)

	require.NoError(t, client.PurgeWorkflowState(ctx, api.InstanceID(parentID)))

	_, err = client.ScheduleNewWorkflow(ctx, "parent", api.WithInstanceID(parentID))
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
}
