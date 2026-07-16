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
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(taskfailed))
}

type taskfailed struct {
	workflow *workflow.Workflow
}

func (tf *taskfailed) Setup(t *testing.T) []framework.Option {
	tf.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(tf.workflow),
	}
}

func (tf *taskfailed) Run(t *testing.T, ctx context.Context) {
	tf.workflow.WaitUntilRunning(t, ctx)

	var activityCalls atomic.Int32

	tf.workflow.Registry().AddWorkflowN("dedup-taskfailed", func(ctx *task.WorkflowContext) (any, error) {
		err := ctx.CallActivity("boom").Await(nil)
		if err == nil {
			return nil, errors.New("activity unexpectedly succeeded")
		}
		return nil, ctx.WaitForSingleEvent("done", time.Minute).Await(nil)
	})
	require.NoError(t, tf.workflow.Registry().AddActivityN("boom", func(ctx task.ActivityContext) (any, error) {
		activityCalls.Add(1)
		return nil, errors.New("planned failure")
	}))

	cl := tf.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "dedup-taskfailed")
	require.NoError(t, err)
	_, err = cl.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int32(1), activityCalls.Load())
		assert.Equal(c, 1, fworkflow.CountHistoryEventsMatching(t, ctx, cl, id, fworkflow.IsTaskFailedFor(0)))
	}, 10*time.Second, 10*time.Millisecond)

	fworkflow.InjectInboxEvent(t, ctx, tf.workflow.DB(), tf.workflow.Dapr(), string(id), &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskFailed{
			TaskFailed: &protos.TaskFailedEvent{
				TaskScheduledId: 0,
				FailureDetails: &protos.TaskFailureDetails{
					ErrorType:    "injected",
					ErrorMessage: "duplicate failure",
				},
			},
		},
	})

	tf.workflow.Dapr().Restart(t, ctx)
	tf.workflow.Dapr().WaitUntilRunning(t, ctx)
	cl = tf.workflow.BackendClient(t, ctx)

	require.NoError(t, cl.RaiseEvent(ctx, id, "done"))

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())

	assert.Equal(t, int32(1), activityCalls.Load(), "activity must not re-run")
	assert.Equal(t, 1, fworkflow.CountHistoryEventsMatching(t, ctx, cl, id, fworkflow.IsTaskFailedFor(0)),
		"history must contain exactly one TaskFailed for taskScheduledId=0")
}
