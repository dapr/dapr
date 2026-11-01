/*
Copyright 2025 The Dapr Authors
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

package deactivate

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(resultwindow))
}

type resultwindow struct {
	workflow             *workflow.Workflow
	completionCalls      atomic.Int64
	a1Calls              atomic.Int64
	a2Calls              atomic.Int64
	completionReached    chan struct{}
	completionReachedAck chan struct{}
}

func (a *resultwindow) Setup(t *testing.T) []framework.Option {
	a.workflow = workflow.New(t,
		workflow.WithFastPath(true),
		workflow.WithDaprdOptions(0, daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_WORKFLOW_JANITOR_PERIOD", "1s",
			"DAPR_WORKFLOW_TEST_ACTIVITY_RESULT_DELAY", "3s",
		))),
	)
	a.completionReached = make(chan struct{}, 1)
	a.completionReachedAck = make(chan struct{}, 1)

	return []framework.Option{
		framework.WithProcesses(a.workflow),
	}
}

func (a *resultwindow) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	worker1Ctx, worker1Cancel := context.WithCancel(ctx)

	a.workflow.Registry().AddWorkflowN("foo", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("a1").Await(nil); err != nil {
			return nil, err
		}
		if err := ctx.CallActivity("a2").Await(nil); err != nil {
			return nil, err
		}
		if a.completionCalls.Load() == 0 {
			close(a.completionReached)
			<-a.completionReachedAck
		}
		a.completionCalls.Add(1)
		return nil, nil
	})
	a.workflow.Registry().AddActivityN("a1", func(task.ActivityContext) (any, error) {
		a.a1Calls.Add(1)
		return "", nil
	})
	a.workflow.Registry().AddActivityN("a2", func(task.ActivityContext) (any, error) {
		a.a2Calls.Add(1)
		return "", nil
	})

	client := client.NewTaskHubGrpcClient(a.workflow.Dapr().GRPCConn(t, ctx), logger.New(t))

	require.NoError(t, client.StartWorkItemListener(worker1Ctx, a.workflow.Registry()))
	t.Cleanup(worker1Cancel)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, a.workflow.Dapr().GetMetadata(t, ctx).ActorRuntime.ActiveActors, a.workflow.ActorTypesCount())
	}, time.Second*10, time.Millisecond*10)

	id, err := client.ScheduleNewWorkflow(ctx, "foo")
	require.NoError(t, err)

	<-a.completionReached
	worker1Cancel()
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, a.workflow.Dapr().GetMetadata(t, ctx).ActorRuntime.ActiveActors)
	}, time.Second*10, time.Millisecond*10)
	close(a.completionReachedAck)

	require.NoError(t, client.StartWorkItemListener(ctx, a.workflow.Registry()))
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, a.workflow.Dapr().GetMetadata(t, ctx).ActorRuntime.ActiveActors, a.workflow.ActorTypesCount())
	}, time.Second*10, time.Millisecond*10)

	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), meta.GetRuntimeStatus().String())

	assert.Equal(t, int64(1), a.a1Calls.Load())
	assert.Equal(t, int64(1), a.a2Calls.Load(), "the re-dispatch must find the settled outcome, not run a2 again")
	assert.Equal(t, int64(2), a.completionCalls.Load())
	assert.GreaterOrEqual(t, a.workflow.Dapr().Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_activity_count", "status:janitor_redispatched"), float64(1))
}
