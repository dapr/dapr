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

package list

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(idsonly))
}

// idsonly asserts that ListInstanceIDs keeps its behaviour next to
// ListInstances, and that the two do not share continuation tokens.
type idsonly struct {
	workflow *workflow.Workflow
}

func (i *idsonly) Setup(t *testing.T) []framework.Option {
	i.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(i.workflow),
	}
}

func (i *idsonly) Run(t *testing.T, ctx context.Context) {
	i.workflow.WaitUntilRunning(t, ctx)

	i.workflow.Registry().AddWorkflowN("done", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	})
	i.workflow.Registry().AddWorkflowN("wait", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.WaitForSingleEvent("go", time.Minute).Await(nil)
	})

	client := i.workflow.BackendClient(t, ctx)
	wf := i.workflow.WorkflowClient(t, ctx)

	ids := []string{"ids-a", "ids-b", "ids-c", "other-a"}
	for n, id := range ids {
		name := "done"
		if n == 1 {
			name = "wait"
		}
		_, err := client.ScheduleNewWorkflow(ctx, name, api.WithInstanceID(api.InstanceID(id)))
		require.NoError(t, err)
		_, err = client.WaitForWorkflowStart(ctx, api.InstanceID(id))
		require.NoError(t, err)
	}

	resp, err := wf.ListInstanceIDs(ctx)
	require.NoError(t, err)
	assert.ElementsMatch(t, ids, resp.InstanceIds)
	assert.Nil(t, resp.ContinuationToken)

	resp, err = wf.ListInstanceIDs(ctx, dworkflow.WithListInstanceIDsPageSize(2))
	require.NoError(t, err)
	assert.Len(t, resp.InstanceIds, 2)
	require.NotNil(t, resp.ContinuationToken)
	idsToken := *resp.ContinuationToken
	rest, err := wf.ListInstanceIDs(ctx, dworkflow.WithListInstanceIDsContinuationToken(idsToken))
	require.NoError(t, err)
	assert.ElementsMatch(t, ids, append(resp.InstanceIds, rest.InstanceIds...))

	resp, err = wf.ListInstanceIDs(ctx, dworkflow.WithListInstanceIDsInstanceIDPrefix("ids-"))
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"ids-a", "ids-b", "ids-c"}, resp.InstanceIds)

	// The same set through ListInstances.
	assert.ElementsMatch(t, ids, fworkflow.ListedIDs(t, ctx, client))
	assert.ElementsMatch(t, []string{"ids-a", "ids-b", "ids-c"},
		fworkflow.ListedIDs(t, ctx, client, api.WithListInstancesInstanceIDPrefix("ids-")))

	// A ListInstanceIDs token is not a ListInstances token.
	_, err = client.ListInstances(ctx, api.WithListInstancesContinuationToken(idsToken))
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}
