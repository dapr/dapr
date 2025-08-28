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

package reconnect

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
		if a.orchestratorCalls.Load() == 0 {
			close(a.workflowStarted)
			<-a.workflowScheduled
			worker1Cancel()
		}
		a.orchestratorCalls.Add(1)
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
	time.Sleep(time.Second)

	// scheduling a workflow with a provided start time
	// this call won't wait for the workflow to start executing
	id, err := client.ScheduleNewOrchestration(ctx, "foo", api.WithStartTime(time.Now()))
	require.NoError(t, err)

	<-a.workflowStarted

	// start another worker so the actors don't get deactivated
	require.NoError(t, client.StartWorkItemListener(ctx, a.workflow.Registry()))

	// TODO add workflows streams count to metadata
	// TODO poll until there are 2 streams
	// closing this channel will trigger closing the first worker
	close(a.workflowScheduled)
	// TODO poll until there are 1 stream

	// verify a worker is still connected by checking the expected registered actors
	time.Sleep(time.Second)

	waitCompletionCtx, waitCompletionCancel := context.WithTimeout(ctx, time.Second*10)
	t.Cleanup(waitCompletionCancel)
	meta, err := client.WaitForOrchestrationCompletion(waitCompletionCtx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), meta.GetRuntimeStatus().String())

	assert.Equal(t, int64(1), a.activityCalls.Load())
	assert.Equal(t, int64(3), a.orchestratorCalls.Load())
}
