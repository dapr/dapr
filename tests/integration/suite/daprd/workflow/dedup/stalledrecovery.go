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

package dedup

import (
	"context"
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
	suite.Register(new(stalledrecovery))
}

// stalledrecovery asserts that a history shaped like the pre-buffering-fix
// stall recovers: a TaskCompleted persisted BEFORE the event that gates the
// matching CallActivity, with the TaskScheduled persisted after. On replay
// the completion arrives while the workflow is still blocked, is buffered,
// and is delivered when the activity is scheduled; the late TaskScheduled in
// history then matches the retained pending action without a nondeterminism
// error. The activity itself blocks forever, so completion proves the
// history resolution was used rather than a re-execution.
type stalledrecovery struct {
	workflow *workflow.Workflow
	blockCh  chan struct{}
}

func (s *stalledrecovery) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t,
		// Signing mode opt-out: the crafted unsigned history event would be rejected by signing verification.
		workflow.WithSigning(false),
	)
	s.blockCh = make(chan struct{})
	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *stalledrecovery) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)
	t.Cleanup(func() { close(s.blockCh) })

	// Sequence numbers: the WaitForSingleEvent synthetic timer takes id 0,
	// so the activity is task id 1.
	require.NoError(t, s.workflow.Registry().AddWorkflowN("dedup-stalledrecovery", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.WaitForSingleEvent("go", time.Hour).Await(nil); err != nil {
			return nil, err
		}
		var out string
		if err := ctx.CallActivity("blocked").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, s.workflow.Registry().AddActivityN("blocked", func(actx task.ActivityContext) (any, error) {
		select {
		case <-s.blockCh:
		case <-actx.Context().Done():
		}
		return "never", actx.Context().Err()
	}))

	cl := s.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "dedup-stalledrecovery")
	require.NoError(t, err)
	_, err = cl.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	// Let the workflow genuinely dispatch the activity so history holds the
	// real EventRaised and TaskScheduled#1; the activity blocks.
	require.NoError(t, cl.RaiseEvent(ctx, id, "go"))
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 1, fworkflow.CountHistoryEventsMatching(t, ctx, cl, id, fworkflow.IsTaskScheduledFor(1)))
	}, 10*time.Second, 10*time.Millisecond)

	// Craft the stalled shape: the completion sits BEFORE the gating event,
	// so on replay it arrives while the workflow is still blocked on the
	// external event wait.
	fworkflow.InsertHistoryEvent(t, ctx, s.workflow.DB(), s.workflow.Dapr(), string(id), &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{
				TaskScheduledId: 1,
				Result:          wrapperspb.String(`"stalled-recovered"`),
			},
		},
	}, fworkflow.IsEventRaisedFor("go"))

	s.workflow.Dapr().Restart(t, ctx)
	s.workflow.Dapr().WaitUntilRunning(t, ctx)
	cl = s.workflow.BackendClient(t, ctx)

	// Nudge a turn; the workflow is not waiting for this event, it only
	// forces a replay of the crafted history.
	require.NoError(t, cl.RaiseEvent(ctx, id, "nudge"))

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.Equal(t, `"stalled-recovered"`, meta.GetOutput().GetValue(),
		"the workflow must complete from the history resolution while the activity stays blocked")
}
