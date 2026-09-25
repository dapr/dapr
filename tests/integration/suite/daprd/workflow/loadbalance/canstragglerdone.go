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
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/grpc"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(canstragglerdone))
}

// canstragglerdone is canstraggler with an orphan that succeeds: its result
// carries the previous scheduling's execution id and must not resolve the
// new generation's task of the same id, whose own result is the output.
type canstragglerdone struct {
	workflow *workflow.Workflow
	logline  [2]*logline.LogLine
}

func (c *canstragglerdone) Setup(t *testing.T) []framework.Option {
	uid, err := uuid.NewRandom()
	require.NoError(t, err)
	wopts := make([]workflow.Option, 0, 3+len(c.logline))
	wopts = append(wopts, workflow.WithDaprds(2), workflow.WithClusteredDeployment(true), workflow.WithFastPath(true))
	for i := range c.logline {
		c.logline[i] = logline.New(t, logline.WithCaptureAll())
		wopts = append(wopts, workflow.WithDaprdOptions(i,
			daprd.WithAppID(uid.String()),
			daprd.WithExecOptions(exec.WithStdout(c.logline[i].Stdout()), exec.WithStderr(c.logline[i].Stderr())),
		))
	}
	c.workflow = workflow.New(t, wopts...)

	return []framework.Option{
		framework.WithProcesses(c.logline[0], c.logline[1], c.workflow),
	}
}

func (c *canstragglerdone) Run(t *testing.T, ctx context.Context) {
	const first, second = "first", "second"
	c.workflow.WaitUntilRunning(t, ctx)

	orphanStarted := make(chan struct{})
	releaseOrphan := make(chan struct{})
	secondStarted := make(chan struct{})
	releaseSecond := make(chan struct{})
	markOrphanStarted := sync.OnceFunc(func() { close(orphanStarted) })
	markSecondStarted := sync.OnceFunc(func() { close(secondStarted) })
	releaseOrphanOnce := sync.OnceFunc(func() { close(releaseOrphan) })
	releaseSecondOnce := sync.OnceFunc(func() { close(releaseSecond) })
	t.Cleanup(releaseOrphanOnce)
	t.Cleanup(releaseSecondOnce)

	require.NoError(t, c.workflow.RegistryN(0).AddWorkflowN("canstragglerdone", func(ctx *task.WorkflowContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}

		if input == first {
			// Scheduled and never awaited: orphaned by the ContinueAsNew.
			ctx.CallActivity("gated", task.WithActivityInput(first))
			if err := ctx.WaitForSingleEvent("proceed", time.Minute).Await(nil); err != nil {
				return nil, err
			}
			ctx.ContinueAsNew(second)
			return nil, nil
		}

		var out string
		if err := ctx.CallActivity("gated", task.WithActivityInput(second)).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	// Re-entrant: the contract is at-least-once, so a re-execution of either
	// body must not panic.
	require.NoError(t, c.workflow.RegistryN(0).AddActivityN("gated", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}

		if input == first {
			markOrphanStarted()
			<-releaseOrphan
			return "done-first", nil
		}
		markSecondStarted()
		<-releaseSecond
		return "done-second", nil
	}))
	_ = c.workflow.BackendClientN(t, ctx, 0)

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.GreaterOrEqual(col,
			len(c.workflow.Dapr().GetMetadata(t, ctx).ActorRuntime.ActiveActors), 3)
	}, time.Second*10, time.Millisecond*10)

	client := client.NewTaskHubGrpcClient(grpc.LoadBalance(t,
		c.workflow.DaprN(0).GRPCConn(t, ctx),
		c.workflow.DaprN(1).GRPCConn(t, ctx),
	), logger.New(t))

	id, err := client.ScheduleNewWorkflow(ctx, "canstragglerdone", api.WithInput(first))
	require.NoError(t, err)

	select {
	case <-orphanStarted:
	case <-time.After(time.Second * 20):
		require.Fail(t, "timed out waiting for the orphaned activity to start")
	}

	require.NoError(t, client.RaiseEvent(ctx, id, "proceed"))
	select {
	case <-secondStarted:
	case <-time.After(time.Second * 20):
		require.Fail(t, "timed out waiting for the second generation's activity to start")
	}

	// The orphan's result carries the previous scheduling's execution id
	// and reaches the workflow before the second generation's result does:
	// the second is released only once the workflow actor has judged the
	// orphan's AddWorkflowEvent under its lock, so the second's completion
	// is serialised behind it.
	// The refusal is observed through the sender's in-hand retry, which
	// only the orphan's own publish can produce: a running workflow refuses
	// no other sender, so nothing else, the "proceed" event included, can
	// satisfy the gate.
	retried := fmt.Sprintf("result publish for workflow '%s' refused, retrying with the result in hand", id)
	count := func(needle string) int { return logline.CountAll(needle, c.logline[:]...) }
	releaseOrphanOnce()
	require.Eventually(t, func() bool { return count(retried) >= 1 }, time.Second*20, time.Millisecond*10,
		"the orphan's result must be refused as superseded and retried in hand")
	releaseSecondOnce()

	metadata, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, metadata.GetRuntimeStatus(), "%v", metadata.GetFailureDetails())
	assert.Equal(t, `"done-second"`, metadata.GetOutput().GetValue())
	fworkflow.WaitNoEventWakeups(t, ctx, c.workflow)
}
