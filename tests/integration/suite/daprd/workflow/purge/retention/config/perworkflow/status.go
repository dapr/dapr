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
package perworkflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(status))
}

// status: a per-workflow entry resolves the status-specific TTL before its
// own anyTerminal, its anyTerminal beats the app-wide TTL for that status,
// and a status it leaves unset falls back to the app-wide TTL for that
// status, not to anyTerminal.
type status struct {
	workflow *workflow.Workflow
}

func (s *status) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t, workflow.WithDaprdOptions(0, policy(t, `
      anyTerminal: "1h"
      failed: "2s"
      workflows:
        - name: ft
          anyTerminal: "1h"
          failed: "1s"
          terminated: "1s"
        - name: fallback
          completed: "1h"
        - name: anyonly
          anyTerminal: "1h"
`)))
	return []framework.Option{framework.WithProcesses(s.workflow)}
}

func (s *status) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	reg := s.workflow.Registry()
	require.NoError(t, reg.AddWorkflowN("ft", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.WaitForSingleEvent("go", time.Minute).Await(nil); err != nil {
			return nil, err
		}
		return nil, errors.New("boom")
	}))
	for _, name := range []string{"fallback", "anyonly"} {
		require.NoError(t, reg.AddWorkflowN(name, func(*task.WorkflowContext) (any, error) {
			return nil, errors.New("boom")
		}))
	}
	cl := s.workflow.BackendClient(t, ctx)
	rows := rowCounter(t, ctx, s.workflow)

	anyonly, err := cl.ScheduleNewWorkflow(ctx, "anyonly")
	require.NoError(t, err)
	meta, err := cl.WaitForWorkflowCompletion(ctx, anyonly)
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_FAILED, meta.GetRuntimeStatus())

	t.Run("failed beats the entry's anyTerminal", func(t *testing.T) {
		id, err := cl.ScheduleNewWorkflow(ctx, "ft")
		require.NoError(t, err)
		require.NoError(t, cl.RaiseEvent(ctx, id, "go"))
		meta, err := cl.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, api.RUNTIME_STATUS_FAILED, meta.GetRuntimeStatus())
		waitPurged(t, rows, string(id), "per-workflow failed TTL did not purge")
	})

	t.Run("terminated beats the entry's anyTerminal", func(t *testing.T) {
		id, err := cl.ScheduleNewWorkflow(ctx, "ft")
		require.NoError(t, err)
		require.NoError(t, cl.TerminateWorkflow(ctx, id))
		meta, err := cl.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, api.RUNTIME_STATUS_TERMINATED, meta.GetRuntimeStatus())
		waitPurged(t, rows, string(id), "per-workflow terminated TTL did not purge")
	})

	t.Run("unset status falls back to the app-wide status TTL", func(t *testing.T) {
		id, err := cl.ScheduleNewWorkflow(ctx, "fallback")
		require.NoError(t, err)
		meta, err := cl.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, api.RUNTIME_STATUS_FAILED, meta.GetRuntimeStatus())
		waitPurged(t, rows, string(id), "app-wide failed TTL did not purge a workflow whose entry only sets completed")
	})

	// fallback was purged by the app-wide 2s failed TTL, which has therefore
	// also elapsed for anyonly; only its own 1h anyTerminal can have kept it.
	assert.Positive(t, rows(t, string(anyonly)), "the entry anyTerminal must beat the app-wide failed TTL")
}
