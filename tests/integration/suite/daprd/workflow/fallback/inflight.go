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

package fallback

import (
	"context"
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
	suite.Register(new(inflight))
}

// inflight disconnects the only worker while one workflow waits on an
// activity in progress and another on a timer. The timer comes due while the
// daprd is a fallback host, which takes no reminders as it has no worker to
// run the turn, and the activity is lost with the worker. Both workflows complete once a worker connects again.
type inflight struct {
	workflow *workflow.Workflow
}

func (i *inflight) Setup(t *testing.T) []framework.Option {
	i.workflow = workflow.New(t,
		workflow.WithStagedOperations(),
		withSettle(t, 0),
	)

	return []framework.Option{
		framework.WithProcesses(i.workflow),
	}
}

func (i *inflight) Run(t *testing.T, ctx context.Context) {
	i.workflow.WaitUntilRunning(t, ctx)

	// The first run of the activity never returns: the worker running it
	// goes away. Later runs return at once.
	var activityRuns atomic.Int32
	hold := make(chan struct{})
	t.Cleanup(func() { close(hold) })

	const timerDelay = time.Second * 2
	var timerArmed atomic.Bool
	var timerArmedAt atomic.Value
	require.NoError(t, i.workflow.Registry().AddWorkflowN("activity", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallActivity("slow").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, i.workflow.Registry().AddActivityN("slow", func(ctx task.ActivityContext) (any, error) {
		if activityRuns.Add(1) == 1 {
			select {
			case <-hold:
			case <-ctx.Context().Done():
			}
			return nil, ctx.Context().Err()
		}
		return "activity-done", nil
	}))
	require.NoError(t, i.workflow.Registry().AddWorkflowN("timer", func(ctx *task.WorkflowContext) (any, error) {
		if !ctx.IsReplaying && !timerArmed.Load() {
			timerArmedAt.Store(time.Now())
			timerArmed.Store(true)
		}
		if err := ctx.CreateTimer(timerDelay).Await(nil); err != nil {
			return nil, err
		}
		return "timer-done", nil
	}))

	worker := i.workflow.ConnectWorker(t, ctx, i.workflow.Registry())
	mgmt := i.workflow.ManagementClient(t, ctx)

	actID, err := mgmt.ScheduleNewWorkflow(ctx, "activity", api.WithInstanceID("fallback-inflight-activity"))
	require.NoError(t, err)
	timerID, err := mgmt.ScheduleNewWorkflow(ctx, "timer", api.WithInstanceID("fallback-inflight-timer"))
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return activityRuns.Load() == 1 && timerArmed.Load()
	}, time.Second*10, time.Millisecond*10)

	worker.Disconnect(t)
	i.workflow.WaitForNoConnectedWorkers(t, ctx)

	// The fallback host serves the status of both while their timer comes due
	// and the activity has no worker to run on.
	octx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()
	for _, id := range []api.InstanceID{actID, timerID} {
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			meta, merr := mgmt.FetchWorkflowMetadata(octx, id)
			if assert.NoError(c, merr) {
				assert.Equal(c, protos.OrchestrationStatus_ORCHESTRATION_STATUS_RUNNING, meta.GetRuntimeStatus())
			}
		}, time.Second*5, time.Millisecond*10)
	}

	// Neither the timer coming due nor the activity's reminder runs anything
	// on the fallback host: the activity does not run again until a worker
	// connects. The window covers the timer's due time and a retry of its
	// reminder.
	window := time.Until(timerArmedAt.Load().(time.Time).Add(timerDelay + time.Second))
	assert.Never(t, func() bool {
		return activityRuns.Load() != 1
	}, max(window, time.Second), time.Millisecond*10, "the activity ran again with no worker connected")

	i.workflow.ConnectWorker(t, ctx, i.workflow.Registry())

	for id, out := range map[api.InstanceID]string{actID: `"activity-done"`, timerID: `"timer-done"`} {
		meta := fworkflow.WaitForCompleted(t, ctx, mgmt, id)
		assert.Equal(t, out, meta.GetOutput().GetValue(), id)
	}
	assert.GreaterOrEqual(t, activityRuns.Load(), int32(2))
}
