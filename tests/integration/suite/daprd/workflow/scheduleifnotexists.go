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

package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(scheduleifnotexists))
}

type scheduleifnotexists struct {
	workflow *workflow.Workflow
}

func (s *scheduleifnotexists) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *scheduleifnotexists) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	s.workflow.Registry().AddWorkflowN("ifnotexists", func(ctx *task.WorkflowContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		if err := ctx.WaitForSingleEvent("continue", time.Minute).Await(nil); err != nil {
			return nil, err
		}
		return input, nil
	})

	client := s.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "ifnotexists", api.WithInstanceID("foo"), api.WithInput("first"))
	require.NoError(t, err)

	// Scheduling with the flag while the instance is active is a no-op instead
	// of an AlreadyExists error.
	_, err = client.ScheduleNewWorkflow(ctx, "ifnotexists", api.WithInstanceID("foo"), api.WithInput("second"), api.WithScheduleIfNotExists())
	require.NoError(t, err)

	require.NoError(t, client.RaiseEvent(ctx, id, "continue"))
	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.Equal(t, `"first"`, meta.GetOutput().GetValue())

	// Scheduling with the flag after completion is also a no-op: the completed
	// instance is not reset and re-run. A restarted instance would be stuck
	// waiting for the "continue" event again, so completion metadata still
	// reporting the first execution proves nothing was scheduled.
	_, err = client.ScheduleNewWorkflow(ctx, "ifnotexists", api.WithInstanceID("foo"), api.WithInput("third"), api.WithScheduleIfNotExists())
	require.NoError(t, err)

	meta, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.Equal(t, `"first"`, meta.GetOutput().GetValue())

	// Without the flag, scheduling over the completed instance still resets and
	// re-runs it (existing behavior is unchanged).
	_, err = client.ScheduleNewWorkflow(ctx, "ifnotexists", api.WithInstanceID("foo"), api.WithInput("fourth"))
	require.NoError(t, err)
	require.NoError(t, client.RaiseEvent(ctx, id, "continue"))
	meta, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, `"fourth"`, meta.GetOutput().GetValue())
}
