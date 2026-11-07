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
	"github.com/dapr/durabletask-go/api/protos"
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

func (w *wait) Setup(t *testing.T) []framework.Option {
	w.waitCh = make(chan struct{})
	// Two daprds form two apps: app 1 hosts the workflow, app 0 only calls
	// it, so the wait below travels the remote actor path, where a plain
	// closed error would surface to the caller as gRPC UNKNOWN.
	w.workflow = workflow.New(t, workflow.WithDaprds(2))

	return []framework.Option{
		framework.WithProcesses(w.workflow),
	}
}

func (w *wait) Run(t *testing.T, ctx context.Context) {
	w.workflow.WaitUntilRunning(t, ctx)

	w.workflow.RegistryN(1).AddWorkflowN("foo", func(ctx *task.WorkflowContext) (any, error) {
		w.called.Add(1)
		<-w.waitCh
		return nil, ctx.CallActivity("bar").Await(nil)
	})
	w.workflow.RegistryN(1).AddActivityN("bar", func(c task.ActivityContext) (any, error) {
		return "", nil
	})

	// Build the client directly (not via BackendClientN, which starts its own
	// work-item listener on the case context): this test must own the only
	// listener so cancelling it really disconnects the worker.
	target := client.NewTaskHubGrpcClient(w.workflow.DaprN(1).GRPCConn(t, ctx), logger.New(t))

	cctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	require.NoError(t, target.StartWorkItemListener(cctx, w.workflow.RegistryN(1)))

	// verify worker is connected by checking the expected registered actors
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, w.workflow.DaprN(1).GetMetadata(t, ctx).ActorRuntime.ActiveActors, w.workflow.ActorTypesCount())
	}, time.Second*10, time.Millisecond*10)

	// Schedule with an explicit start time so the call returns once the
	// instance is created instead of blocking in WaitForInstanceStart: the
	// workflow function blocks on a raw channel before yielding any task, so
	// the instance stays PENDING until the channel closes and the wait-for-
	// start would never resolve on its own.
	id, err := target.ScheduleNewWorkflow(ctx, "foo", api.WithStartTime(time.Now()))
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), w.called.Load())
	}, time.Second*10, time.Millisecond*10)

	// Start a single-shot cross-app wait for completion and let it park its
	// stream on the app-1 workflow actor. The raw stub performs no retries,
	// so whatever the in-flight wait returns is exactly what the
	// deactivation surfaces to the waiter.
	targetAppID := w.workflow.DaprN(1).AppID()
	raw := protos.NewTaskHubSidecarServiceClient(w.workflow.DaprN(0).GRPCConn(t, ctx))
	type waitResult struct {
		resp *protos.GetInstanceResponse
		err  error
	}
	resCh := make(chan waitResult, 1)
	go func() {
		resp, err := raw.WaitForInstanceCompletion(ctx, &protos.GetInstanceRequest{
			InstanceId: string(id),
			Router:     &protos.TaskRouter{TargetAppID: &targetAppID},
		})
		resCh <- waitResult{resp: resp, err: err}
	}()
	time.Sleep(time.Second * 2)

	// Disconnecting the app-1 worker deactivates the workflow actor while
	// the wait is in flight: a transient lifecycle deactivation, not a
	// workflow-driven one, so the in-flight wait must retry across it
	// instead of failing with gRPC UNKNOWN.
	cancel()
	// verify the actor deactivated before reconnecting, so the parked wait
	// really is interrupted rather than sailing through untouched. The
	// engine takes a few seconds to notice the worker is gone, so allow
	// the same 10s window the neighbouring deactivate tests use.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, w.workflow.DaprN(1).GetMetadata(t, ctx).ActorRuntime.ActiveActors)
	}, time.Second*10, time.Millisecond*50)

	// Let the in-flight HaltAll finish closing the parked wait streams
	// before the worker comes back: re-registering too early can keep the
	// actor (and its parked stream) alive, and the wait would then never
	// be interrupted at all.
	time.Sleep(time.Second)

	// Reconnect promptly so the router's retries re-register the wait on the
	// reactivated actor, then release the workflow to run to completion.
	cctx, cancel = context.WithCancel(ctx)
	t.Cleanup(cancel)
	require.NoError(t, target.StartWorkItemListener(cctx, w.workflow.RegistryN(1)))
	close(w.waitCh)

	select {
	case res := <-resCh:
		require.NoError(t, res.err, "in-flight cross-app wait must retry across transient actor deactivation instead of failing")
		assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, res.resp.GetWorkflowState().GetWorkflowStatus())
	case <-time.After(time.Second * 30):
		t.Fatal("timed out waiting for the in-flight wait to return after actor deactivation")
	}

	// The retrying client also observes the terminal status.
	meta, err := target.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())

	// The workflow must have replayed after the deactivation: the function
	// ran once before the disconnect, and a deactivated actor replays it on
	// reconnect. A count of 1 here would mean the wait was never actually
	// interrupted and the test proved nothing.
	assert.GreaterOrEqual(t, w.called.Load(), int64(2), "workflow must have replayed after actor deactivation")
}
