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
	suite.Register(new(limits))
}

// limits checks repeats, expiration and due time.
type limits struct {
	workflow *workflow.Workflow
}

func (l *limits) Setup(t *testing.T) []framework.Option {
	l.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(l.workflow),
	}
}

func (l *limits) Run(t *testing.T, ctx context.Context) {
	l.workflow.WaitUntilRunning(t, ctx)

	l.workflow.Registry().AddWorkflowN("noop", func(*task.WorkflowContext) (any, error) { return nil, nil })
	client := l.workflow.BackendClient(t, ctx)
	owner := wfschedule.Owner(l.workflow.Dapr().Namespace(), l.workflow.Dapr().AppID())

	due := time.Now().Add(2 * time.Second).Truncate(time.Second)
	_, err := client.CreateWorkflowSchedule(ctx, "repeats", "noop", "@every 1s", api.WithScheduleRepeats(2))
	require.NoError(t, err)
	_, err = client.CreateWorkflowSchedule(ctx, "expires", "noop", "@every 1s",
		api.WithScheduleExpirationTime(time.Now().Add(2500*time.Millisecond)))
	require.NoError(t, err)
	_, err = client.CreateWorkflowSchedule(ctx, "due", "noop", "0 0 0 1 1 *", api.WithScheduleDueTime(due))
	require.NoError(t, err)
	assert.Empty(t, wf.ScheduleRuns(t, ctx, client, "due"), "no run before the due time")

	for _, name := range []string{"repeats", "expires"} {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetWorkflowSchedule(ctx, name)
			if assert.NoError(c, err) {
				ws := resp.GetSchedule()
				assert.Equal(c, protos.WorkflowScheduleState_WORKFLOW_SCHEDULE_STATE_COMPLETED, ws.GetStatus().GetState())
				assert.Nil(c, ws.GetStatus().GetNextRunTime())
			}
		}, 20*time.Second, 10*time.Millisecond, name)
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, []string{wfschedule.RunInstanceID("due", owner, due)}, wf.ScheduleRuns(c, ctx, client, "due"))
	}, 20*time.Second, 10*time.Millisecond)

	// A completed schedule has started every run it counts, and no other.
	for name, want := range map[string]uint32{"repeats": 2, "expires": 0} {
		resp, err := client.GetWorkflowSchedule(ctx, name)
		require.NoError(t, err)
		ws := resp.GetSchedule()
		if want > 0 {
			assert.Equal(t, want, ws.GetStatus().GetRunCount())
		} else {
			assert.NotZero(t, ws.GetStatus().GetRunCount(), name)
		}
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, wf.ScheduleRuns(c, ctx, client, name), int(ws.GetStatus().GetRunCount()))
		}, 10*time.Second, 10*time.Millisecond, name)
	}
}
