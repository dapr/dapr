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
	"errors"
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
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

// stragglerbody is the shared body of canstraggler and canstragglerdone. An
// activity orphaned by ContinueAsNew shares its task id with the new
// generation's activity, and its result must not resolve that task whether it
// failed or succeeded. The orphan is released only once the new generation's
// activity is running, and that activity completes only after the orphan's
// result has reached the workflow, so the straggler is the first resolution
// to arrive for task 0 of generation 2.
//
// orphanFails selects which of the two shapes runs: a failure that would fail
// the new generation, or a result that would become its output.
type stragglerbody struct {
	name        string
	orphanFails bool

	workflow *workflow.Workflow
	logline  [2]*logline.LogLine
}

func (s *stragglerbody) setup(t *testing.T) []framework.Option {
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

func (s *stragglerbody) run(t *testing.T, ctx context.Context) {
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

	require.NoError(t, s.workflow.RegistryN(0).AddWorkflowN(s.name, func(wctx *task.WorkflowContext) (any, error) {
		var input string
		if err := wctx.GetInput(&input); err != nil {
			return nil, err
		}

		if input == "first" {
			// Scheduled and never awaited: orphaned by the ContinueAsNew.
			wctx.CallActivity("gated", task.WithActivityInput("first"))
			if err := wctx.WaitForSingleEvent("proceed", time.Minute).Await(nil); err != nil {
				return nil, err
			}
			wctx.ContinueAsNew("second")
			return nil, nil
		}

		var out string
		if err := wctx.CallActivity("gated", task.WithActivityInput("second")).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	// Re-entrant: the contract is at-least-once, so a re-execution of either
	// body must not panic.
	require.NoError(t, s.workflow.RegistryN(0).AddActivityN("gated", func(actx task.ActivityContext) (any, error) {
		var input string
		if err := actx.GetInput(&input); err != nil {
			return nil, err
		}

		if input == "first" {
			markOrphanStarted()
			<-releaseOrphan
			if s.orphanFails {
				return nil, errors.New("orphan failed")
			}
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

	cl := client.NewTaskHubGrpcClient(grpc.LoadBalance(t,
		s.workflow.DaprN(0).GRPCConn(t, ctx),
		s.workflow.DaprN(1).GRPCConn(t, ctx),
	), logger.New(t))

	id, err := cl.ScheduleNewWorkflow(ctx, s.name, api.WithInput("first"))
	require.NoError(t, err)

	select {
	case <-orphanStarted:
	case <-time.After(time.Second * 20):
		require.Fail(t, "timed out waiting for the orphaned activity to start")
	}

	require.NoError(t, cl.RaiseEvent(ctx, id, "proceed"))
	select {
	case <-secondStarted:
	case <-time.After(time.Second * 20):
		require.Fail(t, "timed out waiting for the second generation's activity to start")
	}

	// The orphan's result carries the previous scheduling's execution id and
	// reaches the workflow before the second generation's result does: the
	// second is released only once the workflow actor has judged the orphan's
	// AddWorkflowEvent under its lock, so the second's completion is
	// serialised behind it.
	// The refusal is observed through the sender's in-hand retry, which only
	// the orphan's own publish can produce: a running workflow refuses no
	// other sender, so nothing else, the "proceed" event included, can
	// satisfy the gate.
	retried := fmt.Sprintf("result publish for workflow '%s' refused, retrying with the result in hand", id)
	count := func(needle string) int { return logline.CountAll(needle, s.logline[:]...) }
	releaseOrphanOnce()
	require.Eventually(t, func() bool { return count(retried) >= 1 }, time.Second*20, time.Millisecond*10,
		"the orphan's result must be refused as superseded and retried in hand")
	releaseSecondOnce()

	metadata, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, metadata.GetRuntimeStatus(), "%v", metadata.GetFailureDetails())
	assert.Equal(t, `"done-second"`, metadata.GetOutput().GetValue())
	fworkflow.WaitNoEventWakeups(t, ctx, s.workflow)
}
