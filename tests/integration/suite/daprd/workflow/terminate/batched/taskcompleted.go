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

package batched

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

func (b *taskcompleted) Setup(t *testing.T) []framework.Option {
	b.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(b.workflow),
	}
}

func (b *taskcompleted) Run(t *testing.T, ctx context.Context) {
	b.workflow.WaitUntilRunning(t, ctx)

	var inActivity atomic.Bool
	holdCh := make(chan struct{})

	b.workflow.Registry().AddWorkflowN("terminate-taskcompleted", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("hold").Await(nil); err != nil {
			return nil, err
		}
		return "taskcompleted-done", nil
	})
	require.NoError(t, b.workflow.Registry().AddActivityN("hold", func(ctx task.ActivityContext) (any, error) {
		inActivity.Store(true)
		<-holdCh
		return nil, nil
	}))

	cl := b.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "terminate-taskcompleted")
	require.NoError(t, err)
	_, err = cl.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, inActivity.Load())
		assert.Equal(c, 1, fworkflow.CountHistoryEventsMatching(t, ctx, cl, id, fworkflow.IsTaskScheduledFor(0)))
	}, time.Second*20, time.Millisecond*10)

	fworkflow.InjectInboxEvent(t, ctx, b.workflow.DB(), b.workflow.Dapr(), string(id), &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionTerminated{
			ExecutionTerminated: &protos.ExecutionTerminatedEvent{
				Input: wrapperspb.String(`"stop"`),
			},
		},
	})

	b.workflow.Dapr().Restart(t, ctx)
	close(holdCh)
	b.workflow.Dapr().WaitUntilRunning(t, ctx)
	cl = b.workflow.BackendClient(t, ctx)

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "ORCHESTRATION_STATUS_TERMINATED", meta.GetRuntimeStatus().String())
}
