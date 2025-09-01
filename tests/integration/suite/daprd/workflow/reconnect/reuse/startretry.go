/*
Copyright 2025 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://wwb.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package reuse

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(startretry))
}

type startretry struct {
	workflow          *workflow.Workflow
	orchestratorCalls atomic.Int64
	activityCalls     atomic.Int64
	workflowStarted   chan struct{}
	workflowScheduled chan struct{}
}

func (a *startretry) Setup(t *testing.T) []framework.Option {
	a.workflow = workflow.New(t)
	a.workflowScheduled = make(chan struct{}, 1)
	a.workflowStarted = make(chan struct{}, 1)

	return []framework.Option{
		framework.WithProcesses(a.workflow),
	}
}

func (a *startretry) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	worker1Ctx, worker1Cancel := context.WithCancel(ctx)

	a.workflow.Registry().AddOrchestratorN("foo", func(ctx *task.OrchestrationContext) (any, error) {
		a.orchestratorCalls.Add(1)
		if a.orchestratorCalls.Load() == 1 {
			close(a.workflowStarted)
			<-a.workflowScheduled
		}
		return nil, ctx.CallActivity("bar").Await(nil)
	})
	a.workflow.Registry().AddActivityN("bar", func(c task.ActivityContext) (any, error) {
		a.activityCalls.Add(1)
		return "", nil
	})

	client := client.NewTaskHubGrpcClient(a.workflow.Dapr().GRPCConn(t, ctx), logger.New(t))

	require.NoError(t, client.StartWorkItemListener(worker1Ctx, a.workflow.Registry()))
	t.Cleanup(worker1Cancel)

	// verify worker is connected by checking the expected registered actors
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, a.workflow.Dapr().GetMetadata(t, ctx).ActorRuntime.ActiveActors, 2)
	}, time.Second*10, time.Millisecond*10)

	// scheduling a workflow with a provided start time
	// this call won't wait for the workflow to start executing
	id, err := client.ScheduleNewOrchestration(ctx, "foo", api.WithStartTime(time.Now()))
	require.NoError(t, err)

	<-a.workflowStarted

	// start another worker so the actors don't get deactivated
	require.NoError(t, client.StartWorkItemListener(ctx, a.workflow.Registry()))

	// verify we have 2 connected workers
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 2, a.workflow.Dapr().GetMetadata(t, ctx).Workflows.ConnectedWorkers)
	}, time.Second*10, time.Millisecond*10)

	// stop first worker
	worker1Cancel()
	// verify one worker disconnected
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 1, a.workflow.Dapr().GetMetadata(t, ctx).Workflows.ConnectedWorkers)
	}, time.Second*10, time.Millisecond*10)

	close(a.workflowScheduled)

	// verify a worker is still connected by checking the expected registered actors
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, a.workflow.Dapr().GetMetadata(t, ctx).ActorRuntime.ActiveActors, 2)
	}, time.Second*10, time.Millisecond*10)

	waitCompletionCtx, waitCompletionCancel := context.WithTimeout(ctx, time.Second*10)
	t.Cleanup(waitCompletionCancel)
	meta, err := client.WaitForOrchestrationCompletion(waitCompletionCtx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), meta.GetRuntimeStatus().String())

	assert.Equal(t, int64(1), a.activityCalls.Load())
}
