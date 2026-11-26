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

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(filtertime))
}

// filtertime lists instances by created time and completed time. Each range
// includes its "from" time and excludes its "to" time. A completed-time
// range never matches an instance that did not complete.
type filtertime struct {
	workflow *workflow.Workflow
}

func (f *filtertime) Setup(t *testing.T) []framework.Option {
	f.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(f.workflow),
	}
}

func (f *filtertime) Run(t *testing.T, ctx context.Context) {
	f.workflow.WaitUntilRunning(t, ctx)

	f.workflow.Registry().AddWorkflowN("done", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	})
	f.workflow.Registry().AddWorkflowN("wait", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.WaitForSingleEvent("go", time.Minute).Await(nil)
	})

	client := f.workflow.BackendClient(t, ctx)

	// Batch b starts after batch a completed, and w after batch b.
	const a1, a2, b1, b2, w1 = "a-1", "a-2", "b-1", "b-2", "w-1"
	for _, id := range []string{a1, a2, b1, b2} {
		_, err := client.ScheduleNewWorkflow(ctx, "done", api.WithInstanceID(api.InstanceID(id)))
		require.NoError(t, err)
		_, err = client.WaitForWorkflowCompletion(ctx, api.InstanceID(id))
		require.NoError(t, err)
	}
	_, err := client.ScheduleNewWorkflow(ctx, "wait", api.WithInstanceID(w1))
	require.NoError(t, err)
	_, err = client.WaitForWorkflowStart(ctx, w1)
	require.NoError(t, err)

	byID := make(map[string]*protos.WorkflowInstanceSummary)
	for _, sum := range fworkflow.ListAllInstances(t, ctx, client) {
		byID[sum.GetInstanceId()] = sum
	}
	require.Len(t, byID, 5)
	createdCut := byID[b1].GetCreatedTimestamp().AsTime()
	completedCut := byID[b1].GetCompletedTimestamp().AsTime()
	require.True(t, byID[a2].GetCompletedTimestamp().AsTime().Before(createdCut))
	require.Nil(t, byID[w1].GetCompletedTimestamp())

	assert.ElementsMatch(t, []string{b1, b2, w1},
		fworkflow.ListedIDs(t, ctx, client, api.WithListInstancesCreatedTimeFrom(createdCut)))
	assert.ElementsMatch(t, []string{a1, a2},
		fworkflow.ListedIDs(t, ctx, client, api.WithListInstancesCreatedTimeTo(createdCut)))
	assert.ElementsMatch(t, []string{b1, b2},
		fworkflow.ListedIDs(t, ctx, client, api.WithListInstancesCompletedTimeFrom(completedCut)))
	assert.ElementsMatch(t, []string{a1, a2},
		fworkflow.ListedIDs(t, ctx, client, api.WithListInstancesCompletedTimeTo(completedCut)))
	assert.ElementsMatch(t, []string{b1}, fworkflow.ListedIDs(t, ctx, client,
		api.WithListInstancesCreatedTimeFrom(createdCut),
		api.WithListInstancesCreatedTimeTo(byID[b2].GetCreatedTimestamp().AsTime()),
	))
	assert.ElementsMatch(t, []string{b1, b2}, fworkflow.ListedIDs(t, ctx, client,
		api.WithListInstancesCreatedTimeFrom(createdCut),
		api.WithListInstancesCompletedTimeTo(time.Now().Add(time.Hour)),
	))
}
