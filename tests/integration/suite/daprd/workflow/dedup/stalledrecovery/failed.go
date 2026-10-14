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

package stalledrecovery

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
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(failed))
}

// failed is the TaskFailed sibling of completed: an
// activity failure persisted BEFORE the event that gates the CallActivity,
// with the TaskScheduled persisted after. daprd replays the history with the
// failure moved after its TaskScheduled, so it fails the activity call. The
// activity itself blocks forever, so the workflow failing with the planted
// error proves the history resolution was used.
type failed struct {
	workflow *workflow.Workflow
	blockCh  chan struct{}
}

func (s *failed) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t,
		// Signing mode opt-out: the crafted unsigned history event would be rejected by signing verification.
		workflow.WithSigning(false),
	)
	s.blockCh = make(chan struct{})
	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *failed) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)
	t.Cleanup(func() { close(s.blockCh) })

	// Sequence numbers: the WaitForSingleEvent synthetic timer takes id 0,
	// so the activity is task id 1.
	require.NoError(t, s.workflow.Registry().AddWorkflowN("dedup-stalledrecovery-failed", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.WaitForSingleEvent("go", time.Hour).Await(nil); err != nil {
			return nil, err
		}
		return nil, ctx.CallActivity("blocked").Await(nil)
	}))
	require.NoError(t, s.workflow.Registry().AddActivityN("blocked", func(actx task.ActivityContext) (any, error) {
		select {
		case <-s.blockCh:
		case <-actx.Context().Done():
		}
		return "never", actx.Context().Err()
	}))

	cl := s.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "dedup-stalledrecovery-failed")
	require.NoError(t, err)
	_, err = cl.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	// Let the workflow genuinely dispatch the activity so history holds the
	// real EventRaised and TaskScheduled#1; the activity blocks.
	require.NoError(t, cl.RaiseEvent(ctx, id, "go"))
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 1, fworkflow.CountHistoryEventsMatching(t, ctx, cl, id, fworkflow.IsTaskScheduledFor(1)))
	}, 10*time.Second, 10*time.Millisecond)

	// Craft the stalled shape: the failure sits BEFORE the gating event.
	fworkflow.InsertHistoryEvent(t, ctx, s.workflow.DB(), s.workflow.Dapr(), string(id),
		fworkflow.TaskFailedEvent(1, "injected failure"), fworkflow.IsEventRaisedFor("go"))

	s.workflow.Dapr().Restart(t, ctx)
	s.workflow.Dapr().WaitUntilRunning(t, ctx)
	cl = s.workflow.BackendClient(t, ctx)

	// Nudge a turn; the workflow is not waiting for this event, it only
	// forces a replay of the crafted history.
	require.NoError(t, cl.RaiseEvent(ctx, id, "nudge"))

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "ORCHESTRATION_STATUS_FAILED", meta.GetRuntimeStatus().String())
	assert.Contains(t, meta.GetFailureDetails().GetErrorMessage(), "injected failure",
		"the workflow must fail from the history resolution while the activity stays blocked")
}
