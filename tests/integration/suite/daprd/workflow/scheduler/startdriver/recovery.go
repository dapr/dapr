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

package startdriver

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
	suite.Register(new(recovery))
}

// recovery pins that the start backstop alone recovers a start whose only
// worker vanished mid-turn: with the janitor far out, the durable start
// reminder, due one redrive grace after the start, is the only durable
// re-driver left, and it must complete the workflow after the reconnect.
type recovery struct {
	workflow *workflow.Workflow
	called   atomic.Int64
	held     chan struct{}
	release  chan struct{}
}

func (r *recovery) Setup(t *testing.T) []framework.Option {
	r.held = make(chan struct{}, 1)
	r.release = make(chan struct{})
	r.workflow = workflow.New(t,
		workflow.WithFastPath(true),
		workflow.WithDaprdOptions(0, daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_WORKFLOW_JANITOR_PERIOD", "5m",
			"DAPR_WORKFLOW_PENDING_START_REDRIVE_GRACE", "1s",
		))),
	)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *recovery) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	r.workflow.Registry().AddWorkflowN("held", func(*task.WorkflowContext) (any, error) {
		if r.called.Add(1) == 1 {
			r.held <- struct{}{}
			<-r.release
		}
		return "done", nil
	})

	cl := client.NewTaskHubGrpcClient(r.workflow.Dapr().GRPCConn(t, ctx), logger.New(t))
	worker1Ctx, worker1Cancel := context.WithCancel(ctx)
	require.NoError(t, cl.StartWorkItemListener(worker1Ctx, r.workflow.Registry()))
	t.Cleanup(worker1Cancel)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, r.workflow.Dapr().GetMetadata(t, ctx).ActorRuntime.ActiveActors, r.workflow.ActorTypesCount())
	}, time.Second*10, time.Millisecond*10)

	// A start time keeps the create from waiting on the first commit, which
	// the body holds.
	id, err := cl.ScheduleNewWorkflow(ctx, "held", api.WithStartTime(time.Now()))
	require.NoError(t, err)
	<-r.held

	// The only worker leaves while the start turn is in flight: the local
	// drive cannot retry, so the start is a stranded pending start.
	worker1Cancel()
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, r.workflow.Dapr().GetMetadata(t, ctx).ActorRuntime.ActiveActors)
	}, time.Second*10, time.Millisecond*10)
	close(r.release)

	require.NoError(t, cl.StartWorkItemListener(ctx, r.workflow.Registry()))
	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.GreaterOrEqual(t, r.called.Load(), int64(2), "the backstop must re-drive the start")
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Zero(c, r.workflow.Scheduler().JobKeyCount(t, ctx, "||start-es-"))
	}, time.Second*10, time.Millisecond*10)
}
