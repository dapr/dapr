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
	"fmt"
	"sync/atomic"
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
	suite.Register(new(sameeventname))
}

// sameeventname Selects over two waits for the same event name. Events are
// matched to waiters in creation order, so the first raise completes the
// first task and the second raise completes the one that lost.
type sameeventname struct {
	workflow *workflow.Workflow
	consumed atomic.Bool
}

func (s *sameeventname) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *sameeventname) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	s.workflow.Registry().AddWorkflowN("sameeventname", func(ctx *task.WorkflowContext) (any, error) {
		first := ctx.WaitForSingleEvent("ping", -1)
		second := ctx.WaitForSingleEvent("ping", -1)

		winner, err := ctx.Select(first, second)
		if err != nil {
			return nil, err
		}
		if winner != 0 {
			return nil, fmt.Errorf("expected the oldest waiter to win, got index %d", winner)
		}
		var a int
		if err = first.Await(&a); err != nil {
			return nil, err
		}
		// Signal the test that the first event was consumed, so the second
		// event is raised only once the losing wait is carried into a later
		// turn rather than buffered alongside the first.
		if err = ctx.CallActivity("consumed").Await(nil); err != nil {
			return nil, err
		}

		winner, err = ctx.Select(second)
		if err != nil {
			return nil, err
		}
		if winner != 0 {
			return nil, fmt.Errorf("expected index 0 from a single candidate, got %d", winner)
		}
		var b int
		if err = second.Await(&b); err != nil {
			return nil, err
		}
		return []int{a, b}, nil
	})

	s.workflow.Registry().AddActivityN("consumed", func(task.ActivityContext) (any, error) {
		s.consumed.Store(true)
		return nil, nil
	})

	cl := s.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "sameeventname")
	require.NoError(t, err)
	fworkflow.WaitForWorkflowStartedEvent(t, ctx, cl, id)

	require.NoError(t, cl.RaiseEvent(ctx, id, "ping", api.WithEventPayload(1)))
	require.Eventually(t, s.consumed.Load, time.Second*20, time.Millisecond*10)
	require.NoError(t, cl.RaiseEvent(ctx, id, "ping", api.WithEventPayload(2)))

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.JSONEq(t, `[1,2]`, meta.GetOutput().GetValue())
	assert.Equal(t, 2, fworkflow.CountHistoryEventsOfType[protos.HistoryEvent_EventRaised](t, ctx, cl, id))
}
