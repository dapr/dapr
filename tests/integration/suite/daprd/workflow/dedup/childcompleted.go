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
	suite.Register(new(childcompleted))
}

type childcompleted struct {
	workflow *workflow.Workflow
}

func (cc *childcompleted) Setup(t *testing.T) []framework.Option {
	cc.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(cc.workflow),
	}
}

func (cc *childcompleted) Run(t *testing.T, ctx context.Context) {
	cc.workflow.WaitUntilRunning(t, ctx)

	var childCalls atomic.Int32

	cc.workflow.Registry().AddWorkflowN("dedup-childparent", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallChildWorkflow("dedup-child").Await(nil); err != nil {
			return nil, err
		}
		return nil, ctx.WaitForSingleEvent("done", time.Minute).Await(nil)
	})
	cc.workflow.Registry().AddWorkflowN("dedup-child", func(ctx *task.WorkflowContext) (any, error) {
		childCalls.Add(1)
		return "ok", nil
	})

	cl := cc.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "dedup-childparent")
	require.NoError(t, err)
	_, err = cl.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int32(1), childCalls.Load())
		assert.Equal(c, 1, fworkflow.CountHistoryEventsMatching(t, ctx, cl, id, fworkflow.IsChildCompletedFor(0)))
	}, 15*time.Second, 10*time.Millisecond)

	fworkflow.InjectInboxEvent(t, ctx, cc.workflow.DB(), cc.workflow.Dapr(), string(id), &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ChildWorkflowInstanceCompleted{
			ChildWorkflowInstanceCompleted: &protos.ChildWorkflowInstanceCompletedEvent{
				TaskScheduledId: 0,
				Result:          wrapperspb.String(`"ok"`),
			},
		},
	})

	cc.workflow.Dapr().Restart(t, ctx)
	cc.workflow.Dapr().WaitUntilRunning(t, ctx)
	cl = cc.workflow.BackendClient(t, ctx)

	require.NoError(t, cl.RaiseEvent(ctx, id, "done"))

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())

	assert.Equal(t, int32(1), childCalls.Load(), "child workflow must not re-run")
	assert.Equal(t, 1, fworkflow.CountHistoryEventsMatching(t, ctx, cl, id, fworkflow.IsChildCompletedFor(0)),
		"history must contain exactly one ChildWorkflowInstanceCompleted for taskScheduledId=0")
}
