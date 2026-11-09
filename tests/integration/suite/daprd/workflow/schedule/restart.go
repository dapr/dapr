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

package schedule

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(restart))
}

// restart checks a schedule survives a daprd restart, and that every time it
// missed while no worker was connected still gets its run, unless it skips
// missed times.
type restart struct {
	workflow *workflow.Workflow
}

func (r *restart) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *restart) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	r.workflow.Registry().AddWorkflowN("noop", func(*task.WorkflowContext) (any, error) { return nil, nil })
	// The worker would reconnect to the restarted daprd, so it is stopped.
	wctx, cancel := context.WithCancel(ctx)
	client := r.workflow.BackendClient(t, wctx)

	_, err := client.CreateWorkflowSchedule(ctx, "survivor", "noop", "@every 1s")
	require.NoError(t, err)
	_, err = client.CreateWorkflowSchedule(ctx, "skipper", "noop", "@every 1s", api.WithScheduleSkipMissed())
	require.NoError(t, err)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.NotEmpty(c, wf.ScheduleRuns(t, ctx, client, "survivor"))
		assert.NotEmpty(c, wf.ScheduleRuns(t, ctx, client, "skipper"))
	}, 20*time.Second, 10*time.Millisecond)

	before := len(wf.ScheduleRuns(t, ctx, client, "survivor"))
	cancel()
	// Without a worker the schedules pause, so this misses several times.
	time.Sleep(3 * time.Second)
	r.workflow.Dapr().Restart(t, ctx)
	r.workflow.Dapr().WaitUntilRunning(t, ctx)
	client = r.workflow.BackendClient(t, ctx)

	var runs []string
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		runs = wf.ScheduleRuns(t, ctx, client, "survivor")
		assert.GreaterOrEqual(c, len(runs), before+3)
	}, 20*time.Second, 10*time.Millisecond)
	resp, err := client.GetWorkflowSchedule(ctx, "survivor")
	require.NoError(t, err)
	ws := resp.GetSchedule()
	assert.Equal(t, protos.WorkflowScheduleState_WORKFLOW_SCHEDULE_STATE_ACTIVE, ws.GetStatus().GetState())

	// The restart left the schedule without a worker for a second or more,
	// yet its runs are one second apart from the first to the last.
	var prev time.Time
	for _, id := range runs {
		ts, err := time.Parse("20060102T150405Z", id[len(id)-len("20060102T150405Z"):])
		require.NoError(t, err, id)
		if !prev.IsZero() {
			assert.Equal(t, time.Second, ts.Sub(prev), "no time is skipped: %v", runs)
		}
		prev = ts
	}

	// With skipMissed the missed times get one run, then the schedule carries
	// on from now, so the outage shows as a gap between two runs.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		var gap bool
		var last time.Time
		for _, id := range wf.ScheduleRuns(t, ctx, client, "skipper") {
			ts, err := time.Parse("20060102T150405Z", id[len(id)-len("20060102T150405Z"):])
			if !assert.NoError(c, err, id) {
				return
			}
			gap = gap || (!last.IsZero() && ts.Sub(last) > time.Second)
			last = ts
		}
		assert.True(c, gap, "the missed times are skipped")
	}, 20*time.Second, 10*time.Millisecond)
}
