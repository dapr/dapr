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

package early

import (
	"context"
	"sync"
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
	suite.Register(new(straggler))
}

// straggler asserts that an activity result from a previous generation does
// not resolve the current generation's task with the same ID. Task IDs
// restart on ContinueAsNew. Generation 1 leaves activity "slow" running at id
// 1 and continues as new; generation 2 is part way through, its history not
// yet at id 1, when slow's result arrives. Generation 2 then schedules its
// own task 1, "target". Unless the result is rejected as coming from another
// execution, it is held as an early result and resolves target, whose
// dispatch is then withheld: the workflow returns slow's output and target
// never runs.
type straggler struct {
	workflow *workflow.Workflow

	slowStarted chan struct{}
	gateStarted chan struct{}
	releaseSlow func()
	releaseGate func()
	slowCh      chan struct{}
	gateCh      chan struct{}
	targetCalls atomic.Int32
}

func (s *straggler) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t,
		// Under signing a result for a task the signed history does not hold
		// is refused and retried, never kept, which hides the straggler.
		workflow.WithSigning(false),
		// The test waits on slow's run-activity reminder to know its result
		// was delivered; the fast path elides that reminder.
		workflow.WithFastPath(false),
	)
	s.slowStarted = make(chan struct{}, 1)
	s.gateStarted = make(chan struct{}, 1)
	s.slowCh = make(chan struct{})
	s.gateCh = make(chan struct{})
	s.releaseSlow = sync.OnceFunc(func() { close(s.slowCh) })
	s.releaseGate = sync.OnceFunc(func() { close(s.gateCh) })
	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *straggler) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)
	t.Cleanup(s.releaseSlow)
	t.Cleanup(s.releaseGate)

	reg := s.workflow.Registry()
	// Sequence numbers: generation 1 runs noop at id 0, leaves slow running
	// at id 1 and waits at id 2; generation 2 runs gate at id 0 and target at
	// id 1.
	require.NoError(t, reg.AddWorkflowN("dedup-early-straggler", func(ctx *task.WorkflowContext) (any, error) {
		var gen int
		if err := ctx.GetInput(&gen); err != nil {
			return nil, err
		}
		if gen == 1 {
			if err := ctx.CallActivity("noop").Await(nil); err != nil {
				return nil, err
			}
			ctx.CallActivity("slow")
			if err := ctx.WaitForSingleEvent("can", time.Hour).Await(nil); err != nil {
				return nil, err
			}
			ctx.ContinueAsNew(2)
			return nil, nil
		}
		if err := ctx.CallActivity("gate").Await(nil); err != nil {
			return nil, err
		}
		var out string
		if err := ctx.CallActivity("target").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, reg.AddActivityN("noop", func(task.ActivityContext) (any, error) {
		return nil, nil
	}))
	require.NoError(t, reg.AddActivityN("slow", func(task.ActivityContext) (any, error) {
		s.slowStarted <- struct{}{}
		<-s.slowCh
		return "slow", nil
	}))
	require.NoError(t, reg.AddActivityN("gate", func(task.ActivityContext) (any, error) {
		s.gateStarted <- struct{}{}
		<-s.gateCh
		return nil, nil
	}))
	require.NoError(t, reg.AddActivityN("target", func(task.ActivityContext) (any, error) {
		s.targetCalls.Add(1)
		return "target", nil
	}))

	cl := s.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "dedup-early-straggler", api.WithInput(1))
	require.NoError(t, err)

	wait := func(ch <-chan struct{}, what string) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(20 * time.Second):
			require.Fail(t, what)
		}
	}
	wait(s.slowStarted, "generation 1 never started slow")

	require.NoError(t, cl.RaiseEvent(ctx, id, "can"))
	wait(s.gateStarted, "generation 2 never started gate")
	// gate is dispatched before the turn that continued as new saves.
	fworkflow.WaitForHistoryEvent(t, ctx, cl, id, func(e *protos.HistoryEvent) bool {
		return e.GetExecutionStarted().GetInput().GetValue() == "2"
	})

	// Deliver slow's result while generation 2's history is below id 1, and
	// wait for its run-activity reminder to settle so the result has been
	// judged before generation 2 schedules its own task 1.
	s.releaseSlow()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Zero(c, s.workflow.Scheduler().JobKeyCount(t, ctx, string(id)+"::1::"))
	}, 20*time.Second, 10*time.Millisecond)

	s.releaseGate()

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String(), meta.GetFailureDetails().GetErrorMessage())
	assert.Equal(t, `"target"`, meta.GetOutput().GetValue(), "generation 1's result must not resolve generation 2's task")
	assert.Equal(t, int32(1), s.targetCalls.Load(), "generation 2's task must be dispatched and run once")
}
