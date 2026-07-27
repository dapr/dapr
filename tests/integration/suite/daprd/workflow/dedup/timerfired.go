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
	suite.Register(new(timerfired))
}

type timerfired struct {
	workflow *workflow.Workflow
}

func (tf *timerfired) Setup(t *testing.T) []framework.Option {
	tf.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(tf.workflow),
	}
}

func (tf *timerfired) Run(t *testing.T, ctx context.Context) {
	tf.workflow.WaitUntilRunning(t, ctx)

	tf.workflow.Registry().AddWorkflowN("dedup-timerfired", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CreateTimer(100 * time.Millisecond).Await(nil); err != nil {
			return nil, err
		}
		return nil, ctx.WaitForSingleEvent("done", time.Minute).Await(nil)
	})

	cl := tf.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "dedup-timerfired")
	require.NoError(t, err)
	_, err = cl.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 1, fworkflow.CountHistoryEventsMatching(t, ctx, cl, id, fworkflow.IsTimerFiredFor(0)))
	}, 10*time.Second, 10*time.Millisecond)

	fworkflow.InjectInboxEvent(t, ctx, tf.workflow.DB(), tf.workflow.Dapr(), string(id), &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TimerFired{
			TimerFired: &protos.TimerFiredEvent{
				TimerId: 0,
				FireAt:  timestamppb.Now(),
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

	assert.Equal(t, 1, fworkflow.CountHistoryEventsMatching(t, ctx, cl, id, fworkflow.IsTimerFiredFor(0)),
		"history must contain exactly one TimerFired for timerId=0")
}
