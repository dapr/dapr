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

package chaos

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/proxy"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(terminatecascade))
}

type terminatecascade struct {
	workflow  *workflow.Workflow
	scheduler *scheduler.Scheduler
	proxy     *proxy.Proxy
}

func (c *terminatecascade) Setup(t *testing.T) []framework.Option {
	c.scheduler = scheduler.New(t)
	c.proxy = proxy.New(t, c.scheduler)

	c.workflow = workflow.New(t,
		workflow.WithSchedulerInstance(c.scheduler),
		workflow.WithSchedulerAddress(c.proxy.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(c.scheduler, c.proxy, c.workflow),
	}
}

func (c *terminatecascade) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	const numChildren = 2

	childID := func(i int) string {
		return fmt.Sprintf("terminatecascade-child-%d", i)
	}

	holdCh := make(chan struct{})
	var blocked atomic.Int64
	t.Cleanup(func() {
		select {
		case <-holdCh:
		default:
			close(holdCh)
		}
	})

	r := c.workflow.Registry()

	require.NoError(t, r.AddActivityN("block", func(actx task.ActivityContext) (any, error) {
		blocked.Add(1)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-holdCh:
			return nil, nil
		}
	}))

	require.NoError(t, r.AddWorkflowN("child", func(wctx *task.WorkflowContext) (any, error) {
		return nil, wctx.CallActivity("block").Await(nil)
	}))

	require.NoError(t, r.AddWorkflowN("root", func(wctx *task.WorkflowContext) (any, error) {
		tasks := make([]task.Task, numChildren)
		for i := range numChildren {
			tasks[i] = wctx.CallChildWorkflow("child",
				task.WithChildWorkflowInstanceID(childID(i)),
			)
		}
		for _, tk := range tasks {
			if err := tk.Await(nil); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}))

	cl := c.workflow.BackendClient(t, ctx)

	id, err := cl.ScheduleNewWorkflow(ctx, "root", api.WithInstanceID("terminatecascade-root"))
	require.NoError(t, err)

	require.EventuallyWithT(t, func(co *assert.CollectT) {
		assert.Equal(co, int64(numChildren), blocked.Load())
	}, time.Second*30, time.Millisecond*10)

	termCtx, termCancel := context.WithTimeout(ctx, time.Second*20)
	t.Cleanup(termCancel)
	require.NoError(t, cl.TerminateWorkflow(termCtx, id))

	failedCh := make(chan struct{}, numChildren)
	c.proxy.ArmFailures(proxy.MethodScheduleJob, numChildren, codes.Internal, failedCh)

	for range numChildren {
		select {
		case <-failedCh:
		case <-time.After(time.Second * 15):
			require.Fail(t, "injected ScheduleJob failures never fired")
		}
	}

	require.EventuallyWithT(t, func(co *assert.CollectT) {
		meta, merr := cl.FetchWorkflowMetadata(ctx, id)
		if !assert.NoError(co, merr) {
			return
		}
		assert.Equal(co, api.RUNTIME_STATUS_TERMINATED.String(), meta.GetRuntimeStatus().String())
	}, time.Second*30, time.Millisecond*10)

	for i := range numChildren {
		require.EventuallyWithT(t, func(co *assert.CollectT) {
			meta, merr := cl.FetchWorkflowMetadata(ctx, api.InstanceID(childID(i)))
			if !assert.NoError(co, merr) {
				return
			}
			assert.Equal(co, api.RUNTIME_STATUS_TERMINATED.String(), meta.GetRuntimeStatus().String())
		}, time.Second*60, time.Millisecond*10)
	}

	assert.GreaterOrEqual(t, c.proxy.FailedCount(), numChildren)
}
