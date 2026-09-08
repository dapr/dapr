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

package reuseid

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(staleterminalcache))
}

type staleterminalcache struct {
	workflow *workflow.Workflow
}

func (s *staleterminalcache) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t, workflow.WithDaprds(2))
	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *staleterminalcache) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	var runs atomic.Int64
	s.workflow.Registry().AddWorkflowN("reused", func(ctx *task.WorkflowContext) (any, error) {
		runs.Add(1)
		var in string
		if err := ctx.GetInput(&in); err != nil {
			return nil, err
		}
		return in, nil
	})

	client := s.workflow.BackendClient(t, ctx)
	client2 := s.workflow.BackendClientN(t, ctx, 1)

	const instanceID = "stale-terminal-reuse"
	id, err := client.ScheduleNewWorkflow(ctx, "reused",
		api.WithInstanceID(instanceID), api.WithInput("gen1"))
	require.NoError(t, err)

	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	require.Equal(t, int64(1), runs.Load())

	meta, err = client2.FetchWorkflowMetadata(ctx, id,
		api.WithFetchAppID(s.workflow.Dapr().AppID()))
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())

	s.workflow.WriteWorkflowState(t, ctx, 0, instanceID, 2, nil, []*protos.HistoryEvent{{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{
				Name:  "reused",
				Input: wrapperspb.String(`"gen2"`),
				WorkflowInstance: &protos.WorkflowInstance{
					InstanceId:  instanceID,
					ExecutionId: wrapperspb.String(uuid.New().String()),
				},
			},
		},
	}})

	_, err = s.workflow.Scheduler().Client(t, ctx).ScheduleJob(ctx,
		s.workflow.Scheduler().JobNowActor("new-event-recreated", "default",
			s.workflow.Dapr().AppID(), s.workflow.WorkflowActorType(0), instanceID))
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(2), runs.Load(),
			"the recreated generation's pending start must be re-driven")
	}, time.Second*30, time.Millisecond*10)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, ferr := client.FetchWorkflowMetadata(ctx, id)
		if !assert.NoError(c, ferr) {
			return
		}
		assert.Equal(c, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
		assert.Equal(c, `"gen2"`, meta.GetOutput().GetValue(),
			"the completion must belong to the recreated generation")
	}, time.Second*30, time.Millisecond*10)
}
