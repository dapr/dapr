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
	suite.Register(new(child))
}

// child is the child workflow sibling of completion: a child
// workflow result which reaches the parent before the parent's code has
// scheduled the child must resolve that child call instead of being lost;
// the child itself is never created.
type child struct {
	workflow *workflow.Workflow
}

func (e *child) Setup(t *testing.T) []framework.Option {
	e.workflow = workflow.New(t,
		// Signing mode opt-out: the injected unsigned resolution event would be rejected by signing verification.
		workflow.WithSigning(false),
	)
	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *child) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	var childCalls atomic.Int32

	// Sequence numbers: the WaitForSingleEvent synthetic timer takes id 0,
	// so the child workflow is task id 1.
	require.NoError(t, e.workflow.Registry().AddWorkflowN("dedup-early-child", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.WaitForSingleEvent("go", time.Hour).Await(nil); err != nil {
			return nil, err
		}
		var out string
		if err := ctx.CallChildWorkflow("dedup-early-child-child").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, e.workflow.Registry().AddWorkflowN("dedup-early-child-child", func(ctx *task.WorkflowContext) (any, error) {
		childCalls.Add(1)
		return "child-ran", nil
	}))

	cl := e.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "dedup-early-child")
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
		EventType: &protos.HistoryEvent_ChildWorkflowInstanceCompleted{
			ChildWorkflowInstanceCompleted: &protos.ChildWorkflowInstanceCompletedEvent{
				TaskScheduledId: 1,
				Result:          wrapperspb.String(`"injected-child"`),
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
	assert.Equal(t, `"injected-child"`, meta.GetOutput().GetValue(), "the early result must resolve the child call")

	assert.Equal(t, int32(0), childCalls.Load(), "the child workflow must never execute; its result was injected")
	assert.Equal(t, 0, fworkflow.CountHistoryEventsMatching(t, ctx, cl, id, func(ev *protos.HistoryEvent) bool {
		return ev.GetChildWorkflowInstanceCreated() != nil && ev.GetEventId() == 1
	}), "the resolved child workflow must not be created")
}
