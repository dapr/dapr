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

package reuseid

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
	suite.Register(new(stragglerguard))
}

// stragglerguard terminates a workflow between the arrival of a straggler
// from the generation ContinueAsNew replaced and the result of the current
// scheduling, then reuses the ID. Neither result may be applied: the
// straggler resolves a superseded scheduling and the second reaches a
// terminated workflow; the fresh instance starts with none of them and no
// wake-up is left behind for either.
type stragglerguard struct {
	workflow *workflow.Workflow
	logline  [2]*logline.LogLine
}

func (s *stragglerguard) Setup(t *testing.T) []framework.Option {
	uid, err := uuid.NewRandom()
	require.NoError(t, err)
	wopts := make([]workflow.Option, 0, 3+len(s.logline))
	wopts = append(wopts, workflow.WithDaprds(2), workflow.WithClusteredDeployment(true), workflow.WithFastPath(true))
	for i := range s.logline {
		s.logline[i] = logline.New(t, logline.WithCaptureAll())
		wopts = append(wopts, workflow.WithDaprdOptions(i,
			daprd.WithAppID(uid.String()),
			daprd.WithExecOptions(exec.WithStdout(s.logline[i].Stdout()), exec.WithStderr(s.logline[i].Stderr())),
		))
	}
	s.workflow = workflow.New(t, wopts...)

	return []framework.Option{
		framework.WithProcesses(s.logline[0], s.logline[1], s.workflow),
	}
}

func (s *stragglerguard) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

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

	require.NoError(t, s.workflow.RegistryN(0).AddWorkflowN("stragglerguard", func(ctx *task.WorkflowContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		switch input {
		case "first":
			// Scheduled and never awaited: orphaned by the ContinueAsNew.
			ctx.CallActivity("gated", task.WithActivityInput("first"))
			if err := ctx.WaitForSingleEvent("proceed", time.Minute).Await(nil); err != nil {
				return nil, err
			}
			ctx.ContinueAsNew("second")
			return nil, nil
		case "second":
			var out string
			if err := ctx.CallActivity("gated", task.WithActivityInput("second")).Await(&out); err != nil {
				return nil, err
			}
			return out, nil
		}
		return "fresh", nil
	}))
	// Re-entrant: the contract is at-least-once, so a re-execution of either
	// body must not panic.
	require.NoError(t, s.workflow.RegistryN(0).AddActivityN("gated", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		if input == "first" {
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
		assert.GreaterOrEqual(col,
			len(s.workflow.Dapr().GetMetadata(t, ctx).ActorRuntime.ActiveActors), 3)
	}, time.Second*10, time.Millisecond*10)

	client := client.NewTaskHubGrpcClient(grpc.LoadBalance(t,
		s.workflow.DaprN(0).GRPCConn(t, ctx),
		s.workflow.DaprN(1).GRPCConn(t, ctx),
	), logger.New(t))

	id, err := client.ScheduleNewWorkflow(ctx, "stragglerguard", api.WithInput("first"))
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

	count := func(needle string) int { return logline.CountAll(needle, s.logline[:]...) }
	// Each gate counts a line only the sender it waits on can write. A
	// running workflow refuses no publish but the orphan's, so the in-hand
	// retry is the orphan's alone; and a terminal one acks the orphan with
	// the superseded-scheduling reason instead, so "the workflow has
	// completed" is reachable only by the current scheduling's own result.
	// That line is written after the same critical section clears the
	// ID-reuse guard, so observing it orders the reuse below.
	retried := fmt.Sprintf("result publish for workflow '%s' refused, retrying with the result in hand", id)
	settled := fmt.Sprintf("Workflow actor '%s': dropping completion (sender ''): the workflow has completed", id)

	// The orphan's result is a straggler of the superseded scheduling and
	// is refused; the workflow is then terminated with the current
	// scheduling's result still outstanding.
	releaseOrphanOnce()
	require.Eventually(t, func() bool { return count(retried) >= 1 }, time.Second*20, time.Millisecond*10,
		"the orphan's result must be refused as superseded and retried in hand")
	require.NoError(t, client.TerminateWorkflow(ctx, id))
	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_TERMINATED, meta.GetRuntimeStatus())

	// The current scheduling's result reaches a terminated workflow and is
	// dropped as well; the ID is then reusable.
	releaseSecondOnce()
	require.Eventually(t, func() bool { return count(settled) >= 1 }, time.Second*20, time.Millisecond*10,
		"the current scheduling's result must be dropped by the terminated workflow")
	_, err = client.ScheduleNewWorkflow(ctx, "stragglerguard", api.WithInstanceID(id), api.WithInput("third"))
	require.NoError(t, err, "reusing the ID after the terminated instance's results arrived must succeed")
	meta, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus(), "%v", meta.GetFailureDetails())
	assert.Equal(t, `"fresh"`, meta.GetOutput().GetValue())

	hist, err := client.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	for _, e := range hist.GetEvents() {
		assert.Nil(t, e.GetTaskCompleted(), "no result of the old instance may be in the fresh history")
		assert.Nil(t, e.GetTaskFailed(), "no result of the old instance may be in the fresh history")
	}
	fworkflow.WaitNoEventWakeups(t, ctx, s.workflow)
}
