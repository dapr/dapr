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
	suite.Register(new(suspend))
}

// suspend lists an instance as SUSPENDED after a suspend, as RUNNING after
// the resume, and as COMPLETED at the end.
type suspend struct {
	workflow *workflow.Workflow
}

func (s *suspend) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *suspend) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	s.workflow.Registry().AddWorkflowN("wait", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.WaitForSingleEvent("go", time.Minute).Await(nil)
	})

	client := s.workflow.BackendClient(t, ctx)

	const id = "suspend-1"
	_, err := client.ScheduleNewWorkflow(ctx, "wait", api.WithInstanceID(id))
	require.NoError(t, err)
	_, err = client.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	running := api.WithListInstancesRuntimeStatus(api.RUNTIME_STATUS_RUNNING)
	suspended := api.WithListInstancesRuntimeStatus(api.RUNTIME_STATUS_SUSPENDED)
	completed := api.WithListInstancesRuntimeStatus(api.RUNTIME_STATUS_COMPLETED)

	fworkflow.EventuallyListedIDs(t, ctx, client, []string{id}, running)
	fworkflow.EventuallyListedIDs(t, ctx, client, nil, suspended)

	require.NoError(t, client.SuspendWorkflow(ctx, id, "test"))
	fworkflow.EventuallyListedIDs(t, ctx, client, []string{id}, suspended)
	fworkflow.EventuallyListedIDs(t, ctx, client, nil, running)

	require.NoError(t, client.ResumeWorkflow(ctx, id, "test"))
	fworkflow.EventuallyListedIDs(t, ctx, client, []string{id}, running)
	fworkflow.EventuallyListedIDs(t, ctx, client, nil, suspended)

	require.NoError(t, client.RaiseEvent(ctx, id, "go"))
	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	all := fworkflow.ListAllInstances(t, ctx, client, completed)
	require.Len(t, all, 1)
	fworkflow.AssertSummaryMatchesMetadata(t, ctx, client, all[0])
	assert.Empty(t, fworkflow.ListedIDs(t, ctx, client, running))
}
