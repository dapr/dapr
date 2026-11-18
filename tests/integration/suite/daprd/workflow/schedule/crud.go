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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(crud))
}

// crud checks create conflicts, overwrite, delete, list paging, a failing
// run, and that runs outlive their schedule.
type crud struct {
	workflow *workflow.Workflow
}

func (c *crud) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *crud) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	c.workflow.Registry().AddWorkflowN("noop", func(*task.WorkflowContext) (any, error) { return nil, nil })
	c.workflow.Registry().AddWorkflowN("fails", func(*task.WorkflowContext) (any, error) {
		return nil, assert.AnError
	})
	client := c.workflow.BackendClient(t, ctx)

	_, err := client.CreateWorkflowSchedule(ctx, "a", "noop", "@every 1h")
	require.NoError(t, err)
	_, err = client.CreateWorkflowSchedule(ctx, "a", "noop", "@every 2h")
	assert.Equal(t, codes.AlreadyExists, status.Code(err))

	_, err = client.CreateWorkflowSchedule(ctx, "a", "fails", "@every 1s", api.WithScheduleOverwrite(), api.WithScheduleSkipMissed())
	require.NoError(t, err)
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		resp, gerr := client.GetWorkflowSchedule(ctx, "a")
		if assert.NoError(col, gerr) {
			ws := resp.GetSchedule()
			assert.Equal(col, "fails", ws.GetWorkflowName())
			assert.True(col, ws.GetSkipMissed())
			assert.Equal(col, protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED, ws.GetStatus().GetLastRun().GetRuntimeStatus())
			assert.Contains(col, ws.GetStatus().GetLastRun().GetFailureDetails().GetErrorMessage(), assert.AnError.Error())
			assert.Equal(col, protos.WorkflowScheduleState_WORKFLOW_SCHEDULE_STATE_ACTIVE, ws.GetStatus().GetState(),
				"a failed run does not stop the schedule")
		}
	}, 20*time.Second, 10*time.Millisecond)

	ran := wf.ScheduleRuns(t, ctx, client, "a")
	require.NotEmpty(t, ran)
	_, err = client.DeleteWorkflowSchedule(ctx, "a")
	require.NoError(t, err)
	_, err = client.GetWorkflowSchedule(ctx, "a")
	assert.Equal(t, codes.NotFound, status.Code(err))
	_, err = client.DeleteWorkflowSchedule(ctx, "a")
	assert.Equal(t, codes.NotFound, status.Code(err))
	_, err = client.FetchWorkflowMetadata(ctx, api.InstanceID(ran[0]))
	require.NoError(t, err, "runs outlive their schedule")

	for _, name := range []string{"p1", "p2", "p3"} {
		_, err = client.CreateWorkflowSchedule(ctx, name, "noop", "@every 1h")
		require.NoError(t, err)
	}
	var names []string
	var token *string
	for {
		opts := []api.ListWorkflowSchedulesOptions{api.WithListWorkflowSchedulesPageSize(2)}
		if token != nil {
			opts = append(opts, api.WithListWorkflowSchedulesContinuationToken(*token))
		}
		page, err := client.ListWorkflowSchedules(ctx, opts...)
		require.NoError(t, err)
		assert.LessOrEqual(t, len(page.GetSchedules()), 2)
		for _, ws := range page.GetSchedules() {
			names = append(names, ws.GetName())
		}
		if token = page.ContinuationToken; token == nil {
			break
		}
	}
	assert.ElementsMatch(t, []string{"p1", "p2", "p3"}, names)
}
