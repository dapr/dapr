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

package upgrade

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	wfupgrade "github.com/dapr/dapr/tests/integration/framework/process/workflow/upgrade"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(stalledrecovery))
}

// stalledrecovery has the release daprd write a workflow's history, shapes it
// like the stall releases before v1.19.0 persisted, and expects master to
// recover it. Those releases handed a worker that does not buffer an early
// result the result before the event that gates its activity: the worker
// ignored it, and the daprd persisted the result ahead of the scheduling and
// withheld the dispatch as already resolved, so the workflow waited for good.
// The latest release resolves early results itself, so the test plants that
// shape into the history the release daprd wrote. Master replays the result
// after its scheduling, so the workflow completes from it; the activity, which
// never returns on its own, stays blocked.
type stalledrecovery struct {
	upgrade *wfupgrade.Upgrade
}

func (s *stalledrecovery) Setup(t *testing.T) []framework.Option {
	s.upgrade = wfupgrade.New(t)

	return []framework.Option{
		framework.WithProcesses(s.upgrade),
	}
}

func (s *stalledrecovery) Run(t *testing.T, ctx context.Context) {
	reg := task.NewTaskRegistry()
	// Sequence numbers: the WaitForSingleEvent synthetic timer takes id 0, so
	// the activity is task id 1.
	require.NoError(t, reg.AddWorkflowN("stalled", func(c *task.WorkflowContext) (any, error) {
		if err := c.WaitForSingleEvent("go", time.Hour).Await(nil); err != nil {
			return nil, err
		}
		var out string
		if err := c.CallActivity("blocked").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, reg.AddActivityN("blocked", func(c task.ActivityContext) (any, error) {
		<-c.Context().Done()
		return nil, c.Context().Err()
	}))

	from := s.upgrade.Start(t, ctx, s.upgrade.From(), reg)
	id, err := from.ScheduleNewWorkflow(ctx, "stalled", api.WithInstanceID("stalledrecovery"))
	require.NoError(t, err)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 1, fworkflow.CountHistoryEventsMatching(t, ctx, from, id, fworkflow.IsTimerCreatedFor(0)))
	}, time.Second*20, time.Millisecond*10)

	// The release daprd genuinely dispatches the activity, so its history
	// holds the real EventRaised and TaskScheduled#1; the activity blocks.
	require.NoError(t, from.RaiseEvent(ctx, id, "go"))
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 1, fworkflow.CountHistoryEventsMatching(t, ctx, from, id, fworkflow.IsTaskScheduledFor(1)))
	}, time.Second*20, time.Millisecond*10)
	s.upgrade.From().Kill(t)

	// The stalled shape: the result sits before the gating event, so on replay
	// it arrives while the workflow still waits for the event.
	fworkflow.InsertHistoryEvent(t, ctx, s.upgrade.DB(), s.upgrade.From(), string(id),
		fworkflow.TaskCompletedEvent(1, `"stalled-recovered"`), fworkflow.IsEventRaisedFor("go"))
	events := fworkflow.ReadHistoryEvents(t, ctx, s.upgrade.DB(), string(id))
	completed := slices.IndexFunc(events, fworkflow.IsTaskCompletedFor(1))
	scheduled := slices.IndexFunc(events, fworkflow.IsTaskScheduledFor(1))
	require.True(t, completed >= 0 && completed < scheduled,
		"the result must sit ahead of its scheduling: completed at %d, scheduled at %d", completed, scheduled)

	to := s.upgrade.Start(t, ctx, s.upgrade.To(), reg)
	// Nudge a turn; the workflow is not waiting for this event, it only
	// forces a replay of the stalled history.
	require.NoError(t, to.RaiseEvent(ctx, id, "nudge"))
	meta, err := to.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus(), "%v", meta.GetFailureDetails())
	assert.Equal(t, `"stalled-recovered"`, meta.GetOutput().GetValue(),
		"the workflow must complete from the history resolution while the activity stays blocked")
}
