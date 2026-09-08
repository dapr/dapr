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

package early

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
	suite.Register(new(timer))
}

// timer is the TimerFired sibling of completion: a timer firing
// which reaches the workflow before the workflow's code has created the
// timer must resolve that timer instead of being lost. The workflow's one
// hour timer completes immediately from the injected firing.
type timer struct {
	workflow *workflow.Workflow
}

func (e *timer) Setup(t *testing.T) []framework.Option {
	e.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *timer) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	// Sequence numbers: the WaitForSingleEvent synthetic timer takes id 0,
	// so the durable timer is id 1.
	require.NoError(t, e.workflow.Registry().AddWorkflowN("dedup-early-timer", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.WaitForSingleEvent("go", time.Hour).Await(nil); err != nil {
			return nil, err
		}
		if err := ctx.CreateTimer(time.Hour).Await(nil); err != nil {
			return nil, err
		}
		return "timer-done", nil
	}))

	cl := e.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "dedup-early-timer")
	require.NoError(t, err)
	_, err = cl.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 1, fworkflow.CountHistoryEventsMatching(t, ctx, cl, id, func(ev *protos.HistoryEvent) bool {
			return ev.GetTimerCreated() != nil && ev.GetEventId() == 0
		}))
	}, 10*time.Second, 10*time.Millisecond)

	fworkflow.InjectInboxEvent(t, ctx, e.workflow.DB(), e.workflow.Dapr(), string(id), &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TimerFired{
			TimerFired: &protos.TimerFiredEvent{
				TimerId: 1,
				FireAt:  timestamppb.Now(),
			},
		},
	})

	e.workflow.Dapr().Restart(t, ctx)
	e.workflow.Dapr().WaitUntilRunning(t, ctx)
	cl = e.workflow.BackendClient(t, ctx)

	require.NoError(t, cl.RaiseEvent(ctx, id, "go"))

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.Equal(t, `"timer-done"`, meta.GetOutput().GetValue())

	assert.Equal(t, 0, fworkflow.CountHistoryEventsMatching(t, ctx, cl, id, func(ev *protos.HistoryEvent) bool {
		return ev.GetTimerCreated() != nil && ev.GetEventId() == 1
	}), "the resolved timer must not be created durably")
}
