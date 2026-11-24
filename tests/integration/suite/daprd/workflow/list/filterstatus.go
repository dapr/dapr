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
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(filterstatus))
}

// filterstatus lists instances by runtime status. Each summary has the values
// that GetInstance returns for the instance.
type filterstatus struct {
	workflow *workflow.Workflow
}

func (f *filterstatus) Setup(t *testing.T) []framework.Option {
	f.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(f.workflow),
	}
}

func (f *filterstatus) Run(t *testing.T, ctx context.Context) {
	f.workflow.WaitUntilRunning(t, ctx)

	f.workflow.Registry().AddWorkflowN("done", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	})
	f.workflow.Registry().AddWorkflowN("wait", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.WaitForSingleEvent("go", time.Minute).Await(nil)
	})

	client := f.workflow.BackendClient(t, ctx)

	const run, sus, term = "run-1", "sus-1", "term-1"
	done := []string{"done-1", "done-2"}
	for _, id := range done {
		_, err := client.ScheduleNewWorkflow(ctx, "done", api.WithInstanceID(api.InstanceID(id)))
		require.NoError(t, err)
		_, err = client.WaitForWorkflowCompletion(ctx, api.InstanceID(id))
		require.NoError(t, err)
	}
	for _, id := range []string{run, sus, term} {
		_, err := client.ScheduleNewWorkflow(ctx, "wait", api.WithInstanceID(api.InstanceID(id)))
		require.NoError(t, err)
		_, err = client.WaitForWorkflowStart(ctx, api.InstanceID(id))
		require.NoError(t, err)
	}
	require.NoError(t, client.SuspendWorkflow(ctx, sus, "test"))
	require.NoError(t, client.TerminateWorkflow(ctx, term))
	_, err := client.WaitForWorkflowCompletion(ctx, term)
	require.NoError(t, err)
	fworkflow.EventuallyListedIDs(t, ctx, client, []string{sus}, api.WithListInstancesRuntimeStatus(api.RUNTIME_STATUS_SUSPENDED))

	all := fworkflow.ListAllInstances(t, ctx, client)
	assert.ElementsMatch(t, append([]string{run, sus, term}, done...), fworkflow.SummaryIDs(all))
	for _, sum := range all {
		fworkflow.AssertSummaryMatchesMetadata(t, ctx, client, sum)
	}

	assert.ElementsMatch(t, []string{run},
		fworkflow.ListedIDs(t, ctx, client, api.WithListInstancesRuntimeStatus(api.RUNTIME_STATUS_RUNNING)))
	assert.ElementsMatch(t, done,
		fworkflow.ListedIDs(t, ctx, client, api.WithListInstancesRuntimeStatus(api.RUNTIME_STATUS_COMPLETED)))
	assert.ElementsMatch(t, []string{sus},
		fworkflow.ListedIDs(t, ctx, client, api.WithListInstancesRuntimeStatus(api.RUNTIME_STATUS_SUSPENDED)))
	assert.ElementsMatch(t, []string{term},
		fworkflow.ListedIDs(t, ctx, client, api.WithListInstancesRuntimeStatus(api.RUNTIME_STATUS_TERMINATED)))
	assert.ElementsMatch(t, []string{run, sus},
		fworkflow.ListedIDs(t, ctx, client, api.WithListInstancesRuntimeStatus(api.RUNTIME_STATUS_RUNNING, api.RUNTIME_STATUS_SUSPENDED)))
	assert.Empty(t, fworkflow.ListedIDs(t, ctx, client, api.WithListInstancesRuntimeStatus(api.RUNTIME_STATUS_FAILED)))

	assert.ElementsMatch(t, done[1:], fworkflow.ListedIDs(t, ctx, client, api.WithListInstancesInstanceIDPrefix(done[1])))
}
