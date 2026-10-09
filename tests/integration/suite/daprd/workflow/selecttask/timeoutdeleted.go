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
	suite.Register(new(timeoutdeleted))
}

// timeoutdeleted wins a timed event wait inside Select with an event. daprd
// must delete only that wait's timeout reminder: the losing plain timer is
// still awaitable and stays scheduled, and a newer wait on the same event
// name gets its own reminder and is completed by the next event.
type timeoutdeleted struct {
	workflow *workflow.Workflow
}

func (s *timeoutdeleted) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *timeoutdeleted) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	s.workflow.Registry().AddWorkflowN("timeoutdeleted", func(ctx *task.WorkflowContext) (any, error) {
		first := ctx.WaitForSingleEvent("x", time.Minute)
		timer := ctx.CreateTimer(time.Minute)

		winner, err := ctx.Select(first, timer)
		if err != nil {
			return nil, err
		}
		if winner != 0 {
			return nil, fmt.Errorf("expected the event to win, got index %d", winner)
		}
		var a int
		if err = first.Await(&a); err != nil {
			return nil, err
		}

		var b int
		if err = ctx.WaitForSingleEvent("x", time.Minute).Await(&b); err != nil {
			return nil, fmt.Errorf("second wait on the same name failed: %w", err)
		}
		return []int{a, b}, nil
	})

	cl := s.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "timeoutdeleted")
	require.NoError(t, err)

	// timer-0 is the first wait's timeout, timer-1 the plain Select loser.
	sched := s.workflow.Scheduler()
	one := func(n int) bool { return n == 1 }
	none := func(n int) bool { return n == 0 }
	sched.WaitJobKeyCount(t, ctx, "timer-0", one)
	sched.WaitJobKeyCount(t, ctx, "timer-1", one)

	require.NoError(t, cl.RaiseEvent(ctx, id, "x", api.WithEventPayload(1)))

	// The won wait's timeout is gone, the losing timer is kept, and the
	// second wait armed timer-2.
	sched.WaitJobKeyCount(t, ctx, "timer-0", none)
	sched.WaitJobKeyCount(t, ctx, "timer-2", one)
	sched.WaitJobKeyCount(t, ctx, "timer-1", one)

	require.NoError(t, cl.RaiseEvent(ctx, id, "x", api.WithEventPayload(2)))

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.JSONEq(t, `[1,2]`, meta.GetOutput().GetValue())

	sched.WaitJobKeyCount(t, ctx, "timer-", none)
}
