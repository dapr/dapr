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

package selecttask

import (
	"context"
	"testing"

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
	suite.Register(new(suspendbatch))
}

// suspendbatch raises both events of a Select while the workflow is
// suspended, second candidate first. On resume both complete in the same
// poll, and the lowest index wins regardless of arrival order.
type suspendbatch struct {
	workflow *workflow.Workflow
}

func (s *suspendbatch) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *suspendbatch) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	s.workflow.Registry().AddWorkflowN("suspendbatch", func(ctx *task.WorkflowContext) (any, error) {
		a := ctx.WaitForSingleEvent("a", -1)
		b := ctx.WaitForSingleEvent("b", -1)
		return ctx.Select(a, b)
	})

	cl := s.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "suspendbatch")
	require.NoError(t, err)
	fworkflow.WaitForWorkflowStartedEvent(t, ctx, cl, id)

	require.NoError(t, cl.SuspendWorkflow(ctx, id, "hold"))
	fworkflow.WaitForRuntimeStatus(t, ctx, cl, id, protos.OrchestrationStatus_ORCHESTRATION_STATUS_SUSPENDED)

	require.NoError(t, cl.RaiseEvent(ctx, id, "b", api.WithEventPayload("b")))
	require.NoError(t, cl.RaiseEvent(ctx, id, "a", api.WithEventPayload("a")))
	fworkflow.WaitForHistoryEvent(t, ctx, cl, id, fworkflow.IsEventRaisedFor("a"))
	fworkflow.WaitForRuntimeStatus(t, ctx, cl, id, protos.OrchestrationStatus_ORCHESTRATION_STATUS_SUSPENDED)

	require.NoError(t, cl.ResumeWorkflow(ctx, id, "go"))

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.JSONEq(t, `0`, meta.GetOutput().GetValue(), "the lowest index must win when both candidates complete in the resumed batch")
}
