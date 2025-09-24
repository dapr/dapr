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
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(crossactivity))
}

type crossactivity struct {
	workflow *workflow.Workflow
	calledA  atomic.Int64
	calledB  atomic.Int64
	waitCh   chan struct{}
}

func (c *crossactivity) Setup(t *testing.T) []framework.Option {
	c.waitCh = make(chan struct{})
	c.workflow = workflow.New(t,
		workflow.WithDaprds(2),
	)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *crossactivity) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	c.workflow.Registry().AddOrchestratorN("foo", func(ctx *task.OrchestrationContext) (any, error) {
		if err := ctx.CallActivity("bar").Await(nil); err != nil {
			return nil, err
		}
		return nil, ctx.CallActivity("xyz").Await(nil)
	})
	c.workflow.Registry().AddActivityN("bar", func(ctx task.ActivityContext) (any, error) {
		c.calledA.Add(1)
		<-c.waitCh
		return "", nil
	})
	c.workflow.Registry().AddActivityN("xyz", func(task.ActivityContext) (any, error) {
		c.calledB.Add(1)
		return "", nil
	})

	cl := client.NewTaskHubGrpcClient(c.workflow.DaprN(0).GRPCConn(t, ctx), logger.New(t))

	cctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	require.NoError(t, cl.StartWorkItemListener(cctx, c.workflow.Registry()))

	// verify worker is connected by checking the expected registered actors
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.Len(col, c.workflow.Dapr().GetMetadata(t, ctx).ActorRuntime.ActiveActors, 2)
	}, time.Second*10, time.Millisecond*10)

	id, err := cl.ScheduleNewOrchestration(ctx, "foo")
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(cc *assert.CollectT) {
		assert.Equal(cc, int64(1), c.calledA.Load())
	}, time.Second*10, time.Millisecond*10)

	cancel()
	// verify worker is disconnected by checking the expected registered actors
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.Empty(col, c.workflow.Dapr().GetMetadata(t, ctx).ActorRuntime.ActiveActors)
	}, time.Second*10, time.Millisecond*10)
	close(c.waitCh)

	cl = client.NewTaskHubGrpcClient(c.workflow.Dapr().GRPCConn(t, ctx), logger.New(t))
	cctx, cancel = context.WithCancel(ctx)
	t.Cleanup(cancel)
	require.NoError(t, cl.StartWorkItemListener(cctx, c.workflow.Registry()))

	waitCompletionCtx, waitCompletionCancel := context.WithTimeout(ctx, time.Second*10)
	t.Cleanup(waitCompletionCancel)
	meta, err := cl.WaitForOrchestrationCompletion(waitCompletionCtx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())

	assert.Equal(t, int64(2), c.calledA.Load())
	assert.Equal(t, int64(1), c.calledB.Load())
}
