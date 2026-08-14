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

package loadbalance

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/grpc"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(keycollision))
}

type keycollision struct {
	workflow *workflow.Workflow
}

func (k *keycollision) Setup(t *testing.T) []framework.Option {
	k.workflow = workflow.NewClustered(t, 2)

	return []framework.Option{
		framework.WithProcesses(k.workflow),
	}
}

func (k *keycollision) Run(t *testing.T, ctx context.Context) {
	k.workflow.WaitUntilRunning(t, ctx)

	var activityStarted atomic.Bool
	releaseActivity := make(chan struct{})
	var blockerStarted atomic.Bool
	releaseBlocker := make(chan struct{})
	t.Cleanup(func() {
		for _, ch := range []chan struct{}{releaseActivity, releaseBlocker} {
			select {
			case <-ch:
			default:
				close(ch)
			}
		}
	})

	require.NoError(t, k.workflow.RegistryN(0).AddWorkflowN("withactivity", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallActivity("gated").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, k.workflow.RegistryN(0).AddActivityN("gated", func(ctx task.ActivityContext) (any, error) {
		activityStarted.Store(true)
		<-releaseActivity
		return "activity-done", nil
	}))
	require.NoError(t, k.workflow.RegistryN(0).AddWorkflowN("blocker", func(ctx *task.WorkflowContext) (any, error) {
		// The first turn must complete so the instance leaves PENDING and
		// the schedule call returns; only the second turn blocks, keeping
		// its workflow-task waiter registered.
		if err := ctx.WaitForSingleEvent("proceed", time.Minute).Await(nil); err != nil {
			return nil, err
		}
		blockerStarted.Store(true)
		<-releaseBlocker
		return "blocker-done", nil
	}))
	_ = k.workflow.BackendClientN(t, ctx, 0)

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.GreaterOrEqual(col,
			len(k.workflow.Dapr().GetMetadata(t, ctx).ActorRuntime.ActiveActors), 3)
	}, time.Second*10, time.Millisecond*10)

	client := client.NewTaskHubGrpcClient(grpc.LoadBalance(t,
		k.workflow.DaprN(0).GRPCConn(t, ctx),
		k.workflow.DaprN(1).GRPCConn(t, ctx),
	), logger.New(t))

	// Activity task 0 of "collide" rendezvouses under the activity actor ID
	// "collide::0".
	_, err := client.ScheduleNewWorkflow(ctx, "withactivity", api.WithInstanceID("collide"))
	require.NoError(t, err)
	require.Eventually(t, activityStarted.Load, time.Second*20, time.Millisecond*10)

	// A workflow whose instance ID is that same string; while its blocked
	// turn is in flight, its workflow-task waiter shares the executor actor
	// ID with the activity waiter above.
	_, err = client.ScheduleNewWorkflow(ctx, "blocker", api.WithInstanceID("collide::0"))
	require.NoError(t, err)
	require.NoError(t, client.RaiseEvent(ctx, "collide::0", "proceed"))
	require.Eventually(t, blockerStarted.Load, time.Second*20, time.Millisecond*10)

	// Complete the activity while the blocker's turn is still pending: the
	// activity completion must reach the activity waiter, not the blocker's
	// workflow-task waiter.
	close(releaseActivity)

	waitCtx, cancel := context.WithTimeout(ctx, time.Second*30)
	t.Cleanup(cancel)
	meta, err := client.WaitForWorkflowCompletion(waitCtx, "collide")
	require.NoError(t, err)
	assert.Equal(t, `"activity-done"`, meta.GetOutput().GetValue())

	close(releaseBlocker)
	meta, err = client.WaitForWorkflowCompletion(ctx, "collide::0")
	require.NoError(t, err)
	assert.Equal(t, `"blocker-done"`, meta.GetOutput().GetValue())
}
