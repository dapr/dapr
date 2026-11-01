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

package wakev2

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
	suite.Register(new(noescalation))
}

// noescalation pins that a failed local drive is recovered by the janitor
// alone: no per-event one-shot reminder is created for it, before or after the
// worker returns.
type noescalation struct {
	workflow *workflow.Workflow
	a1Calls  atomic.Int64
	bodyRuns atomic.Int64
	reached  chan struct{}
	release  chan struct{}
}

func (n *noescalation) Setup(t *testing.T) []framework.Option {
	n.reached = make(chan struct{}, 1)
	n.release = make(chan struct{})
	n.workflow = workflow.New(t,
		workflow.WithFastPath(true),
		workflow.WithDaprdOptions(0, daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_WORKFLOW_JANITOR_PERIOD", "2s",
		))),
	)

	return []framework.Option{
		framework.WithProcesses(n.workflow),
	}
}

func (n *noescalation) Run(t *testing.T, ctx context.Context) {
	n.workflow.WaitUntilRunning(t, ctx)

	n.workflow.Registry().AddWorkflowN("foo", func(wctx *task.WorkflowContext) (any, error) {
		if err := wctx.CallActivity("a1").Await(nil); err != nil {
			return nil, err
		}
		if n.bodyRuns.Add(1) == 1 {
			n.reached <- struct{}{}
			<-n.release
		}
		return "done", nil
	})
	n.workflow.Registry().AddActivityN("a1", func(task.ActivityContext) (any, error) {
		n.a1Calls.Add(1)
		return "", nil
	})

	cl := client.NewTaskHubGrpcClient(n.workflow.Dapr().GRPCConn(t, ctx), logger.New(t))
	worker1Ctx, worker1Cancel := context.WithCancel(ctx)
	require.NoError(t, cl.StartWorkItemListener(worker1Ctx, n.workflow.Registry()))
	t.Cleanup(worker1Cancel)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, n.workflow.Dapr().GetMetadata(t, ctx).ActorRuntime.ActiveActors, n.workflow.ActorTypesCount())
	}, time.Second*10, time.Millisecond*10)

	oneShots := func() int {
		return n.workflow.Scheduler().JobKeyCount(t, ctx, "||new-event-") - n.workflow.Scheduler().JobKeyCount(t, ctx, "||new-event-janitor")
	}

	id, err := cl.ScheduleNewWorkflow(ctx, "foo")
	require.NoError(t, err)
	<-n.reached

	// The turn driven by a1's completion is in flight when its worker leaves:
	// the local drive fails and must not be escalated to a durable reminder.
	worker1Cancel()
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, n.workflow.Dapr().GetMetadata(t, ctx).ActorRuntime.ActiveActors)
	}, time.Second*10, time.Millisecond*10)
	close(n.release)
	assert.Never(t, func() bool { return oneShots() > 0 }, time.Second*3, time.Millisecond*10,
		"a failed local drive was escalated to a per-event reminder")

	require.NoError(t, cl.StartWorkItemListener(ctx, n.workflow.Registry()))
	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.Equal(t, int64(1), n.a1Calls.Load())
	// A drive that could not be armed at all falls back to a durable one-shot,
	// which acks and self-deletes on its empty-inbox fire.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Zero(c, oneShots())
	}, time.Second*10, time.Millisecond*10)
	assert.Zero(t, n.workflow.Dapr().Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_wake_count", "status:escalated"))
}
