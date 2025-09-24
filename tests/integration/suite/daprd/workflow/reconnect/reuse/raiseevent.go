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
	suite.Register(new(raiseevent))
}

type raiseevent struct {
	workflow    *workflow.Workflow
	called      atomic.Int64
	awaiting    chan struct{}
	awaitingAck chan struct{}
}

func (r *raiseevent) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t)
	r.awaiting = make(chan struct{}, 1)
	r.awaitingAck = make(chan struct{}, 1)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *raiseevent) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	r.workflow.Registry().AddOrchestratorN("foo", func(ctx *task.OrchestrationContext) (any, error) {
		r.called.Add(1)
		task := ctx.WaitForSingleEvent("event1", 1*time.Minute)
		if r.called.Load() == 1 {
			close(r.awaiting)
			<-r.awaitingAck
		}
		return nil, task.Await(nil)
	})

	client := client.NewTaskHubGrpcClient(r.workflow.Dapr().GRPCConn(t, ctx), logger.New(t))

	cctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	require.NoError(t, client.StartWorkItemListener(cctx, r.workflow.Registry()))

	// verify worker is connected by checking the expected registered actors
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, r.workflow.Dapr().GetMetadata(t, ctx).ActorRuntime.ActiveActors, 2)
	}, time.Second*10, time.Millisecond*10)

	// scheduling a workflow with a provided start time
	// this call won't wait for the workflow to start executing
	id, err := client.ScheduleNewOrchestration(ctx, "foo", api.WithStartTime(time.Now()))
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), r.called.Load())
	}, time.Second*10, time.Millisecond*10)

	<-r.awaiting

	// start another worker so the actors don't get deactivated
	require.NoError(t, client.StartWorkItemListener(ctx, r.workflow.Registry()))
	// verify we have 2 connected workers
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 2, r.workflow.Dapr().GetMetadata(t, ctx).Workflows.ConnectedWorkers)
	}, time.Second*10, time.Millisecond*10)

	// stop first worker
	cancel()
	// verify one worker disconnected
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 1, r.workflow.Dapr().GetMetadata(t, ctx).Workflows.ConnectedWorkers)
	}, time.Second*10, time.Millisecond*10)

	close(r.awaitingAck)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.NoError(c, client.RaiseEvent(ctx, id, "event1"))
	}, time.Second*10, time.Millisecond*10)

	waitCompletionCtx, waitCompletionCancel := context.WithTimeout(ctx, time.Second*10)
	t.Cleanup(waitCompletionCancel)
	meta, err := client.WaitForOrchestrationCompletion(waitCompletionCtx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
}
