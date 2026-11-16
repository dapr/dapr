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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	wfschedule "github.com/dapr/dapr/pkg/runtime/wfengine/inprocess/schedule/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(trigger))
}

// trigger checks a triggered run starts at once and leaves the next
// scheduled run where it was, that triggers naming one run ID start one run,
// and that a trigger naming an ID outside the schedule's runs gets its own.
type trigger struct {
	workflow *workflow.Workflow
}

func (r *trigger) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *trigger) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	r.workflow.Registry().AddWorkflowN("noop", func(*task.WorkflowContext) (any, error) { return "done", nil })
	client := r.workflow.BackendClient(t, ctx)

	_, err := client.CreateWorkflowSchedule(ctx, "hourly", "noop", "@every 1h")
	require.NoError(t, err)
	before, err := client.GetWorkflowSchedule(ctx, "hourly")
	require.NoError(t, err)
	next := before.GetSchedule().GetStatus().GetNextRunTime().AsTime()
	require.False(t, next.IsZero())

	triggered, err := client.TriggerWorkflowSchedule(ctx, "hourly")
	require.NoError(t, err)
	id := api.InstanceID(triggered.GetInstanceId())
	assert.True(t, strings.HasPrefix(string(id), "hourly_") && strings.HasSuffix(string(id), "_manual"), id)
	md, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, md.GetRuntimeStatus())

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, gerr := client.GetWorkflowSchedule(ctx, "hourly")
		if assert.NoError(c, gerr) {
			ws := resp.GetSchedule()
			assert.Equal(c, uint32(1), ws.GetStatus().GetRunCount())
			assert.Equal(c, string(id), ws.GetStatus().GetLastRun().GetInstanceId())
			assert.True(c, next.Equal(ws.GetStatus().GetNextRunTime().AsTime()), "a trigger does not move the next run")
		}
	}, 10*time.Second, 10*time.Millisecond)
	assert.Equal(t, []string{string(id)}, wf.ScheduleRuns(t, ctx, client, "hourly"))

	dup := "hourly_" + wfschedule.Owner(r.workflow.Dapr().Namespace(), r.workflow.Dapr().AppID()) + "_dup_manual"
	for _, runID := range []string{dup, dup, "dapr-internal-schedule-x"} {
		require.NoError(t, client.RaiseEvent(ctx, api.InstanceID(wfschedule.InstanceIDPrefix+"hourly"), "dapr.internal.schedule.trigger",
			api.WithEventPayload(map[string]string{"instanceId": runID})))
	}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, gerr := client.GetWorkflowSchedule(ctx, "hourly")
		if !assert.NoError(c, gerr) {
			return
		}
		st := resp.GetSchedule().GetStatus()
		assert.Equal(c, uint32(3), st.GetRunCount())
		runs := wf.ScheduleRuns(c, ctx, client, "hourly")
		assert.Len(c, runs, 3)
		assert.Contains(c, runs, dup)
		last := st.GetLastRun().GetInstanceId()
		assert.True(c, last != dup && last != string(id) && strings.HasSuffix(last, "_manual"), last)
		assert.Contains(c, runs, last)
	}, 10*time.Second, 10*time.Millisecond)

	_, err = client.TriggerWorkflowSchedule(ctx, "missing")
	assert.Equal(t, codes.NotFound, status.Code(err))

	// A triggered run counts toward the repeats, so this one uses them up.
	_, err = client.CreateWorkflowSchedule(ctx, "once", "noop", "@every 1h", api.WithScheduleRepeats(1))
	require.NoError(t, err)
	_, err = client.TriggerWorkflowSchedule(ctx, "once")
	require.NoError(t, err)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		_, terr := client.TriggerWorkflowSchedule(ctx, "once")
		assert.Equal(c, codes.FailedPrecondition, status.Code(terr))
	}, 10*time.Second, 10*time.Millisecond)
	assert.Len(t, wf.ScheduleRuns(t, ctx, client, "once"), 1)
}
