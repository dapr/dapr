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
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(activityvstimer))
}

// activityvstimer races a blocked activity against a short timer. The
// activity is released only after the timer has fired, so the timer always
// wins; the losing activity is then awaited in a later turn and its result
// returned, so losers stay awaitable after Select.
type activityvstimer struct {
	workflow *workflow.Workflow
	calls    atomic.Int64
	release  chan struct{}
	once     sync.Once
}

func (a *activityvstimer) Setup(t *testing.T) []framework.Option {
	a.workflow = workflow.New(t)
	a.release = make(chan struct{})

	return []framework.Option{
		framework.WithProcesses(a.workflow),
	}
}

func (a *activityvstimer) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	a.workflow.Registry().AddWorkflowN("activityvstimer", func(ctx *task.WorkflowContext) (any, error) {
		slow := ctx.CallActivity("slow")
		timer := ctx.CreateTimer(time.Millisecond * 500)

		winner, err := ctx.Select(slow, timer)
		if err != nil {
			return nil, err
		}
		if winner != 1 {
			return nil, fmt.Errorf("expected the timer to win, got index %d", winner)
		}

		var result string
		if err := slow.Await(&result); err != nil {
			return nil, err
		}
		return result, nil
	})
	a.workflow.Registry().AddActivityN("slow", func(task.ActivityContext) (any, error) {
		a.calls.Add(1)
		<-a.release
		return "slow-done", nil
	})

	release := func() { a.once.Do(func() { close(a.release) }) }
	t.Cleanup(release)

	cl := a.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "activityvstimer")
	require.NoError(t, err)

	fworkflow.WaitForHistoryEvent(t, ctx, cl, id, func(e *protos.HistoryEvent) bool {
		return e.GetTimerFired() != nil
	})
	release()

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.JSONEq(t, `"slow-done"`, meta.GetOutput().GetValue())
	assert.Equal(t, int64(1), a.calls.Load())
}
