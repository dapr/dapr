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

package deactivate

import (
	"context"
	"fmt"
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
	suite.Register(new(wait))
}

type wait struct {
	workflow *workflow.Workflow
	called   atomic.Int64
	waitCh   chan struct{}
}

func (a *wait) Setup(t *testing.T) []framework.Option {
	a.waitCh = make(chan struct{})
	// Asserts the workflow actor types are unregistered once the worker
	// leaves, which the staged operations preview does not do.
	a.workflow = workflow.New(t, workflow.WithStagedOperations(false))

	return []framework.Option{
		framework.WithProcesses(a.workflow),
	}
}

func (a *wait) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	a.workflow.Registry().AddWorkflowN("foo", func(ctx *task.WorkflowContext) (any, error) {
		a.called.Add(1)
		<-a.waitCh
		return nil, ctx.CallActivity("bar").Await(nil)
	})
	a.workflow.Registry().AddActivityN("bar", func(c task.ActivityContext) (any, error) {
		return "ok", nil
	})

	c := client.NewTaskHubGrpcClient(a.workflow.Dapr().GRPCConn(t, ctx), logger.New(t))

	cctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	require.NoError(t, c.StartWorkItemListener(cctx, a.workflow.Registry()))

	// verify worker is connected by checking the expected registered actors
	assert.EventuallyWithT(t, func(coll *assert.CollectT) {
		assert.Len(coll, a.workflow.Dapr().GetMetadata(t, ctx).ActorRuntime.ActiveActors, a.workflow.ActorTypesCount())
	}, time.Second*10, time.Millisecond*10)

	// scheduling a workflow with a provided start time
	// this call won't wait for the workflow to start executing
	id, err := c.ScheduleNewWorkflow(ctx, "foo", api.WithStartTime(time.Now()))
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(coll *assert.CollectT) {
		assert.Equal(coll, int64(1), a.called.Load())
	}, time.Second*10, time.Millisecond*10)

	// Start WaitForWorkflowCompletion while the workflow is in flight, BEFORE actor deactivation.
	waitErrCh := make(chan error, 1)
	go func() {
		meta, err := c.WaitForWorkflowCompletion(ctx, id)
		if err != nil {
			waitErrCh <- err
			return
		}
		if meta.GetRuntimeStatus() != api.RUNTIME_STATUS_COMPLETED {
			waitErrCh <- fmt.Errorf("unexpected runtime status: %v", meta.GetRuntimeStatus())
			return
		}
		waitErrCh <- nil
	}()

	// Allow the stream to establish
	time.Sleep(100 * time.Millisecond)

	// Disconnect the worker, causing actor lifecycle deactivation of the workflow orchestrator.
	cancel()
	// verify worker is disconnected by checking the expected registered actors
	assert.EventuallyWithT(t, func(coll *assert.CollectT) {
		assert.Empty(coll, a.workflow.Dapr().GetMetadata(t, ctx).ActorRuntime.ActiveActors)
	}, time.Second*10, time.Millisecond*10)
	close(a.waitCh)

	// Reconnect worker
	cctx, cancel = context.WithCancel(ctx)
	t.Cleanup(cancel)
	require.NoError(t, c.StartWorkItemListener(cctx, a.workflow.Registry()))

	// Verify the in-flight wait retries on Unavailable and eventually returns the terminal status
	select {
	case err := <-waitErrCh:
		require.NoError(t, err)
	case <-time.After(time.Second * 30):
		t.Fatal("timed out waiting for in-flight WaitForWorkflowCompletion to complete")
	}
}
