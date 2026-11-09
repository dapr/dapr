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
	suite.Register(new(basic))
}

// basic creates a schedule and checks its runs start on time as detached
// workflows, and that Get and List report them.
type basic struct {
	workflow *workflow.Workflow
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	b.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(b.workflow),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	b.workflow.WaitUntilRunning(t, ctx)

	b.workflow.Registry().AddWorkflowN("echo", func(ctx *task.WorkflowContext) (any, error) {
		var in string
		if err := ctx.GetInput(&in); err != nil {
			return nil, err
		}
		return "ran:" + in, nil
	})
	client := b.workflow.BackendClient(t, ctx)

	created := time.Now()
	_, err := client.CreateWorkflowSchedule(ctx, "basic", "echo", "@every 1s", api.WithScheduleInput("hi"))
	require.NoError(t, err)

	var ids []string
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		ids = wf.ScheduleRuns(t, ctx, client, "basic")
		assert.GreaterOrEqual(c, len(ids), 2)
	}, 20*time.Second, 10*time.Millisecond)

	for i, id := range ids {
		ts, perr := time.Parse("20060102T150405Z", id[len(id)-len("20060102T150405Z"):])
		require.NoError(t, perr, id)
		assert.False(t, ts.Before(created.Truncate(time.Second)), id)
		if i > 0 {
			assert.NotEqual(t, ids[i-1], id)
		}
	}

	md, err := client.WaitForWorkflowCompletion(ctx, api.InstanceID(ids[0]), api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, md.GetRuntimeStatus())
	assert.JSONEq(t, `"ran:hi"`, md.GetOutput().GetValue())

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, gerr := client.GetWorkflowSchedule(ctx, "basic")
		if !assert.NoError(c, gerr) {
			return
		}
		ws := resp.GetSchedule()
		assert.Equal(c, "echo", ws.GetWorkflowName())
		assert.Equal(c, "@every 1s", ws.GetSchedule())
		assert.Nil(c, ws.SkipMissed, "catching up is the default")
		assert.JSONEq(c, `"hi"`, ws.GetInput().GetValue())
		st := ws.GetStatus()
		assert.Equal(c, protos.WorkflowScheduleState_WORKFLOW_SCHEDULE_STATE_ACTIVE, st.GetState())
		assert.GreaterOrEqual(c, st.GetRunCount(), uint32(2))
		assert.True(c, st.GetNextRunTime().AsTime().After(st.GetLastRunTime().AsTime()))
		assert.Equal(c, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, st.GetLastRun().GetRuntimeStatus())
		assert.JSONEq(c, `"ran:hi"`, st.GetLastRun().GetOutput().GetValue())
		assert.False(c, st.GetLastRun().GetLastUpdatedTimestamp().AsTime().Before(st.GetLastRun().GetCreatedTimestamp().AsTime()))
	}, 10*time.Second, 10*time.Millisecond)

	list, err := client.ListWorkflowSchedules(ctx)
	require.NoError(t, err)
	require.Len(t, list.GetSchedules(), 1)
	assert.Equal(t, "basic", list.GetSchedules()[0].GetName())

	// The schedule is itself a workflow instance, with each run recorded in
	// its history.
	hist, err := client.GetInstanceHistory(ctx, "dapr-internal-schedule-basic")
	require.NoError(t, err)
	var spawned int
	for _, e := range hist.GetEvents() {
		if e.GetDetachedWorkflowInstanceCreated() != nil {
			spawned++
		}
	}
	assert.Equal(t, 1, spawned, "each generation starts one run and continues as new")
}
