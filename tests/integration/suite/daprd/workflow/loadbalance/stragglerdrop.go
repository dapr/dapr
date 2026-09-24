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
	"bytes"
	"context"
	"fmt"
	"sync"
	"sync/atomic"
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
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(stragglerdrop))
}

// stragglerdrop verifies the sender side of a superseded activity result: an
// activity orphaned by ContinueAsNew whose result names the previous
// scheduling is refused, retried with the result in hand for the sender's
// window, and then dropped. Its body is never re-executed and no durable
// reminder is left behind for it. The window is shortened so the drop is
// observable inside the case budget.
type stragglerdrop struct {
	workflow *workflow.Workflow
	logline  [2]*logline.LogLine
}

func (s *stragglerdrop) Setup(t *testing.T) []framework.Option {
	uid, err := uuid.NewRandom()
	require.NoError(t, err)
	wopts := make([]workflow.Option, 0, 3+len(s.logline))
	wopts = append(wopts, workflow.WithDaprds(2), workflow.WithClusteredDeployment(true), workflow.WithFastPath(true))
	for i := range s.logline {
		s.logline[i] = logline.New(t, logline.WithCaptureAll())
		wopts = append(wopts, workflow.WithDaprdOptions(i,
			daprd.WithAppID(uid.String()),
			daprd.WithExecOptions(
				exec.WithStdout(s.logline[i].Stdout()), exec.WithStderr(s.logline[i].Stderr()),
				exec.WithEnvVars(t, "DAPR_WORKFLOW_TEST_ACTIVITY_PUBLISH_RETRY_WINDOW", "2s"),
			),
		))
	}
	s.workflow = workflow.New(t, wopts...)

	return []framework.Option{
		framework.WithProcesses(s.logline[0], s.logline[1], s.workflow),
	}
}

func (s *stragglerdrop) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)
	const first, second = "first", "second"

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
	var orphanRuns atomic.Int32

	require.NoError(t, s.workflow.RegistryN(0).AddWorkflowN("stragglerdrop", func(ctx *task.WorkflowContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		if input == first {
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
	require.NoError(t, s.workflow.RegistryN(0).AddActivityN("gated", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		if input == first {
			orphanRuns.Add(1)
			markOrphanStarted()
			<-releaseOrphan
			return "done-first", nil
		}
		markSecondStarted()
		<-releaseSecond
		return "done-second", nil
	}))
	_ = s.workflow.BackendClientN(t, ctx, 0)

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.GreaterOrEqual(col, len(s.workflow.Dapr().GetMetadata(t, ctx).ActorRuntime.ActiveActors), 3)
	}, time.Second*10, time.Millisecond*10)

	client := client.NewTaskHubGrpcClient(grpc.LoadBalance(t,
		s.workflow.DaprN(0).GRPCConn(t, ctx),
		s.workflow.DaprN(1).GRPCConn(t, ctx),
	), logger.New(t))

	id, err := client.ScheduleNewWorkflow(ctx, "stragglerdrop", api.WithInput(first))
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
	// As in canstraggler: the orphan's result, carrying the previous
	// scheduling's execution id, reaches the workflow while generation 2's
	// activity is still running, and is refused as superseded. The second
	// result is released only once that refusal has been retried in hand.
	count := func(needle string) int {
		n := 0
		for _, l := range s.logline {
			n += bytes.Count(l.StdoutBuffer(), []byte(needle))
		}
		return n
	}
	retried := fmt.Sprintf("result publish for workflow '%s' refused, retrying with the result in hand", id)
	dropped := fmt.Sprintf("dropping the result for workflow '%s', still superseded after the retry window", id)
	// Generation 2's activity stays held until the drop: once its own
	// result is in history the straggler would be absorbed as a duplicate
	// instead, which is harmless but not the path under test.
	releaseOrphanOnce()
	require.Eventually(t, func() bool { return count(retried) >= 1 }, time.Second*20, time.Millisecond*10,
		"the orphan's result must be refused as superseded and retried in hand")
	require.Eventually(t, func() bool { return count(dropped) >= 1 }, time.Second*20, time.Millisecond*50,
		"the superseded result must be dropped by its sender once the window expires")
	// Generation 2's dispatch on the shared activity actor may already have
	// aborted and retried the orphan's execution (at-least-once); what the
	// drop guarantees is that nothing runs it again from here.
	runsAtDrop := orphanRuns.Load()
	releaseSecondOnce()

	metadata, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED, metadata.GetRuntimeStatus(), "%v", metadata.GetFailureDetails())
	require.Equal(t, `"done-second"`, metadata.GetOutput().GetValue())

	// The drop is terminal: the orphan's body does not run again and nothing
	// durable is left to re-deliver it.
	orphanActivity := string(id) + "::0::"
	s.workflow.Scheduler().WaitJobKeyCount(t, ctx, orphanActivity, func(n int) bool { return n == 0 })
	s.workflow.Scheduler().WaitJobKeyCount(t, ctx, "new-event", func(n int) bool { return n == 0 })
	time.Sleep(time.Second * 3)
	assert.Equal(t, runsAtDrop, orphanRuns.Load(), "a dropped straggler must not be re-executed")
	assert.Equal(t, 1, count(dropped), "the straggler must be dropped once")
}
