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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(suspendtimer))
}

// suspendtimer suspends a workflow blocked in Select before its timer
// candidate fires. The fired timer is held until resume, after which the
// timer wins.
type suspendtimer struct {
	workflow *workflow.Workflow
}

func (s *suspendtimer) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *suspendtimer) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	s.workflow.Registry().AddWorkflowN("suspendtimer", func(ctx *task.WorkflowContext) (any, error) {
		event := ctx.WaitForSingleEvent("x", -1)
		timer := ctx.CreateTimer(time.Second)
		return ctx.Select(event, timer)
	})

	cl := s.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "suspendtimer")
	require.NoError(t, err)
	fworkflow.WaitForHistoryEvent(t, ctx, cl, id, func(e *protos.HistoryEvent) bool {
		return e.GetTimerCreated() != nil
	})

	require.NoError(t, cl.SuspendWorkflow(ctx, id, "hold"))
	fworkflow.WaitForRuntimeStatus(t, ctx, cl, id, protos.OrchestrationStatus_ORCHESTRATION_STATUS_SUSPENDED)

	// Outlive the timer while suspended. The instance must stay suspended.
	time.Sleep(time.Second * 2)
	meta, err := cl.FetchWorkflowMetadata(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_SUSPENDED", meta.GetRuntimeStatus().String())

	require.NoError(t, cl.ResumeWorkflow(ctx, id, "go"))

	meta, err = cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.JSONEq(t, `1`, meta.GetOutput().GetValue())
}
