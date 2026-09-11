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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(completion))
}

// completion asserts that an activity result which reaches the workflow
// before the workflow's code has scheduled the activity resolves that
// activity instead of being lost. The completion is planted in the persisted
// inbox while the workflow is blocked on an external event, so the turn
// triggered by raising the event processes [TaskCompleted#1, EventRaised] in
// that order: the completion arrives at the replay before the code reaches
// CallActivity, and the same turn then re-emits the ScheduleTask action.
// Before the buffering fix the engine silently discarded the completion and
// the actor backend suppressed the re-dispatch as already resolved,
// deadlocking the workflow forever; this is the deterministic reproduction
// of the scheduler crash redelivery stall seen in clustered deployment mode.
// With the fix the buffered resolution completes the activity task as it is
// scheduled, the activity is never dispatched, and the workflow completes
// with the injected result.
type completion struct {
	workflow *workflow.Workflow
}

func (e *completion) Setup(t *testing.T) []framework.Option {
	e.workflow = workflow.New(t,
		// Signing mode opt-out: the injected unsigned resolution event would be rejected by signing verification.
		workflow.WithSigning(false),
	)
	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *completion) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	var activityCalls atomic.Int32

	// Sequence numbers: the WaitForSingleEvent synthetic timer takes id 0,
	// so the activity is task id 1.
	require.NoError(t, e.workflow.Registry().AddWorkflowN("dedup-early-completion", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.WaitForSingleEvent("go", time.Hour).Await(nil); err != nil {
			return nil, err
		}
		var out string
		if err := ctx.CallActivity("step").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, e.workflow.Registry().AddActivityN("step", func(task.ActivityContext) (any, error) {
		activityCalls.Add(1)
		return "ran", nil
	}))

	cl := e.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "dedup-early-completion")
	require.NoError(t, err)
	_, err = cl.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	// Wait for the first turn to persist so the event numbering is settled:
	// the external event wait's synthetic timer occupies id 0.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 1, fworkflow.CountHistoryEventsMatching(t, ctx, cl, id, func(ev *protos.HistoryEvent) bool {
			return ev.GetTimerCreated() != nil && ev.GetEventId() == 0
		}))
	}, 10*time.Second, 10*time.Millisecond)

	// Plant the activity result for task id 1 in the persisted inbox while
	// the workflow is idle, then restart daprd to invalidate the actor's
	// in-memory cache. The next turn, triggered by RaiseEvent, drains the
	// inbox as [TaskCompleted#1, EventRaised] in one work item: the
	// completion is processed before the code reaches CallActivity, and the
	// activity is scheduled later in the same replay.
	fworkflow.InjectInboxEvent(t, ctx, e.workflow.DB(), e.workflow.Dapr(), string(id), &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{
				TaskScheduledId: 1,
				Result:          wrapperspb.String(`"injected"`),
			},
		},
	})

	e.workflow.Dapr().Restart(t, ctx)
	e.workflow.Dapr().WaitUntilRunning(t, ctx)
	cl = e.workflow.BackendClient(t, ctx)

	require.NoError(t, cl.RaiseEvent(ctx, id, "go"))

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.Equal(t, `"injected"`, meta.GetOutput().GetValue(), "the early result must resolve the activity")

	assert.Equal(t, int32(0), activityCalls.Load(), "the activity must never execute; its result was injected")
	// The suppressed ScheduleTask action means no TaskScheduled event is ever
	// appended, and a consumed completion with no matching TaskScheduled is
	// stripped from the persisted history; neither event may appear.
	assert.Equal(t, 0, fworkflow.CountHistoryEventsMatching(t, ctx, cl, id, fworkflow.IsTaskScheduledFor(1)),
		"the resolved activity must not be dispatched")
}
