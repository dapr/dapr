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
	suite.Register(new(stragglerterminal))
}

// stragglerterminal verifies that a straggler arriving at a workflow whose
// history is already terminal is acked on its first call instead of being
// refused recoverably: a terminal history can never gain the scheduling the
// completion is missing, so the verdict is final and the sender must not
// spend its whole retry window taking the terminal instance's turn lock once
// a second to be told the same thing. Generation 2 replaces the orphan's task
// id with a timer, so its history passes that id without ever scheduling or
// resolving a task there.
type stragglerterminal struct {
	workflow *workflow.Workflow
	logline  [2]*logline.LogLine
}

func (s *stragglerterminal) Setup(t *testing.T) []framework.Option {
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

func (s *stragglerterminal) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)
	const first, second = "first", "second"

	orphanStarted := make(chan struct{})
	releaseOrphan := make(chan struct{})
	markOrphanStarted := sync.OnceFunc(func() { close(orphanStarted) })
	releaseOrphanOnce := sync.OnceFunc(func() { close(releaseOrphan) })
	t.Cleanup(releaseOrphanOnce)

	require.NoError(t, s.workflow.RegistryN(0).AddWorkflowN("stragglerterminal", func(ctx *task.WorkflowContext) (any, error) {
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
		// A timer, not an activity: generation 2 must take id 0 without
		// scheduling a task there, so the straggler is judged by the
		// passed-id rule rather than deduped against a resolution of its
		// own id, and the orphan's in-flight execution is left alone.
		if err := ctx.CreateTimer(time.Millisecond * 100).Await(nil); err != nil {
			return nil, err
		}
		return "done-" + second, nil
	}))
	// Re-entrant: the contract is at-least-once, so a re-execution of the
	// body must not panic.
	require.NoError(t, s.workflow.RegistryN(0).AddActivityN("gated", func(ctx task.ActivityContext) (any, error) {
		markOrphanStarted()
		<-releaseOrphan
		return "done-first", nil
	}))
	_ = s.workflow.BackendClientN(t, ctx, 0)

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.GreaterOrEqual(col, len(s.workflow.Dapr().GetMetadata(t, ctx).ActorRuntime.ActiveActors), 3)
	}, time.Second*10, time.Millisecond*10)

	client := client.NewTaskHubGrpcClient(grpc.LoadBalance(t,
		s.workflow.DaprN(0).GRPCConn(t, ctx),
		s.workflow.DaprN(1).GRPCConn(t, ctx),
	), logger.New(t))

	id, err := client.ScheduleNewWorkflow(ctx, "stragglerterminal", api.WithInput(first))
	require.NoError(t, err)
	select {
	case <-orphanStarted:
	case <-time.After(time.Second * 20):
		require.Fail(t, "timed out waiting for the orphaned activity to start")
	}
	require.NoError(t, client.RaiseEvent(ctx, id, "proceed"))

	// Generation 2 holds nothing, so the workflow reaches its terminal
	// history while the orphan is still held and has published nothing.
	metadata, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED, metadata.GetRuntimeStatus(), "%v", metadata.GetFailureDetails())
	require.Equal(t, `"done-second"`, metadata.GetOutput().GetValue())

	count := func(needle string) int { return logline.CountAll(needle, s.logline[:]...) }
	retried := fmt.Sprintf("result publish for workflow '%s' refused, retrying with the result in hand", id)
	dropped := fmt.Sprintf("Workflow actor '%s': dropping completion (sender", id)
	windowDrop := fmt.Sprintf("dropping the result for workflow '%s', still superseded after the retry window", id)
	require.Equal(t, 0, count(retried), "nothing may have been refused before the orphan's result is published")

	releaseOrphanOnce()
	require.Eventually(t, func() bool { return count(dropped) >= 1 }, time.Second*10, time.Millisecond*10,
		"the terminal workflow must ack the straggler on its first call")
	assert.Equal(t, 0, count(retried), "the straggler must not be refused recoverably by a terminal workflow")
	assert.Equal(t, 0, count(windowDrop), "an acked straggler never reaches its sender's window expiry")

	// The ack is terminal: nothing durable is left to re-deliver the result.
	s.workflow.Scheduler().WaitJobKeyCount(t, ctx, string(id)+"::0::", func(n int) bool { return n == 0 })
	fworkflow.WaitNoEventWakeups(t, ctx, s.workflow)
}
