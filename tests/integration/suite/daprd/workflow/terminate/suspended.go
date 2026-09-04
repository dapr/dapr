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

package terminate

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(suspended))
}

// suspended verifies that terminating a suspended workflow terminates it. The
// suspension must neither buffer the terminate nor suppress the termination
// action the workflow produces for it.
type suspended struct {
	workflow *workflow.Workflow
}

func (s *suspended) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *suspended) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	s.workflow.Registry().AddWorkflowN("terminate-suspended", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.WaitForSingleEvent("never", time.Hour).Await(nil); err != nil {
			return nil, err
		}
		return "suspended-done", nil
	})

	cl := s.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "terminate-suspended", api.WithInstanceID("terminate-suspended"))
	require.NoError(t, err)
	_, err = cl.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	require.NoError(t, cl.SuspendWorkflow(ctx, id, "hold it"))
	meta, err := cl.FetchWorkflowMetadata(ctx, id)
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_SUSPENDED.String(), meta.GetRuntimeStatus().String())

	termCtx, termCancel := context.WithTimeout(ctx, time.Second*20)
	t.Cleanup(termCancel)
	require.NoError(t, cl.TerminateWorkflow(termCtx, id))

	meta, err = cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_TERMINATED.String(), meta.GetRuntimeStatus().String())
}
