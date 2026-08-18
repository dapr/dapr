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
	suite.Register(new(taskcompleted))
}

type taskcompleted struct {
	workflow *workflow.Workflow
}

func (tc *taskcompleted) Setup(t *testing.T) []framework.Option {
	tc.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(tc.workflow),
	}
}

func (tc *taskcompleted) Run(t *testing.T, ctx context.Context) {
	tc.workflow.WaitUntilRunning(t, ctx)

	var activityCalls atomic.Int32

	tc.workflow.Registry().AddWorkflowN("dedup-taskcompleted", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("inc").Await(nil); err != nil {
			return nil, err
		}
		return nil, ctx.WaitForSingleEvent("done", time.Minute).Await(nil)
	})
	require.NoError(t, tc.workflow.Registry().AddActivityN("inc", func(ctx task.ActivityContext) (any, error) {
		activityCalls.Add(1)
		return nil, nil
	}))

	cl := tc.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "dedup-taskcompleted")
	require.NoError(t, err)
	_, err = cl.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int32(1), activityCalls.Load())
		assert.Equal(c, 1, fworkflow.CountHistoryEventsMatching(t, ctx, cl, id, fworkflow.IsTaskCompletedFor(0)))
	}, 10*time.Second, 10*time.Millisecond)

	fworkflow.InjectInboxEvent(t, ctx, tc.workflow.DB(), tc.workflow.Dapr(), string(id), &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{
				TaskScheduledId: 0,
				Result:          wrapperspb.String(`null`),
			},
		},
	})

	tc.workflow.Dapr().Restart(t, ctx)
	tc.workflow.Dapr().WaitUntilRunning(t, ctx)
	cl = tc.workflow.BackendClient(t, ctx)

	require.NoError(t, cl.RaiseEvent(ctx, id, "done"))

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())

	assert.Equal(t, int32(1), activityCalls.Load(), "activity must not re-run")
	assert.Equal(t, 1, fworkflow.CountHistoryEventsMatching(t, ctx, cl, id, fworkflow.IsTaskCompletedFor(0)),
		"history must contain exactly one TaskCompleted for taskScheduledId=0")
}
