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

package terminalcost

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/fault"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

// fanOut is the fan-out leftoverWakeups completes in one turn.
const fanOut = 200

// fullLoadKey matches a row every full load reads and the settled check does
// not: that check reads the first history row, so history rows cannot tell
// the two apart.
const fullLoadKey = "||propagated-history"

// TerminalCost bounds what a completed workflow costs to poke: every call a
// completed instance can still receive in bulk must be answered without
// reading its whole history from the store each time. Those reads run one
// after another under the actor lock, so their cost grows with the history
// and the number of calls, and the client's completion wait queues behind
// them.
//
// It runs one daprd with WorkflowsFastPath off (the fast path arms no
// per-result wake-ups) on a fault store that counts and slows full loads.
// Signing follows the env.
type TerminalCost struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
	log      *logline.LogLine

	started atomic.Int32
	gate    chan struct{}
}

func New(t *testing.T) *TerminalCost {
	t.Helper()
	os.SkipWindows(t)

	c := &TerminalCost{
		store: fault.New(t),
		log:   logline.New(t, logline.WithCaptureAll()),
	}
	var storeOpts []daprd.Option
	c.ss, storeOpts = workflow.FaultStore(t, c.store)
	c.workflow = workflow.New(t,
		workflow.WithNoDB(),
		workflow.WithFastPath(false),
		workflow.WithDaprdOptions(0, append(storeOpts,
			daprd.WithAppID(uuid.New().String()),
			daprd.WithExecOptions(exec.WithStdout(c.log.Stdout()), exec.WithStderr(c.log.Stderr())),
		)...),
	)
	c.register(t)
	return c
}

func (c *TerminalCost) Run(t *testing.T, ctx context.Context) {
	t.Helper()
	c.log.Run(t, ctx)
	c.ss.Run(t, ctx)
	c.workflow.Run(t, ctx)
}

func (c *TerminalCost) Cleanup(t *testing.T) {
	t.Helper()
	c.workflow.Cleanup(t)
	c.ss.Cleanup(t)
	c.log.Cleanup(t)
}

// Execute runs every check, each as a subtest.
func (c *TerminalCost) Execute(t *testing.T, ctx context.Context) {
	t.Helper()
	c.workflow.WaitUntilRunning(t, ctx)
	bc := c.workflow.BackendClient(t, ctx)

	t.Run("leftover wake-ups after a fan-out completed in one turn", func(t *testing.T) {
		c.leftoverWakeups(t, ctx, bc)
	})
	t.Run("late duplicate results after completion", func(t *testing.T) {
		c.lateDuplicates(t, ctx, bc)
	})
}

// register registers the workflows the checks start, before the worker
// starts, and makes every full load cost what a large history does on a real
// store. 200 leftover reloads then sleep for 10 s on their own, above the
// completion-wait bound in leftoverWakeups however loaded the runner is.
func (c *TerminalCost) register(t *testing.T) {
	t.Helper()

	// Hold every fan-out activity until all of them run, so the results
	// arrive together and queue ahead of the first wake-up.
	c.gate = make(chan struct{})
	r := c.workflow.Registry()
	require.NoError(t, r.AddActivityN("terminalcost-gated", func(task.ActivityContext) (any, error) {
		if c.started.Add(1) == fanOut {
			close(c.gate)
		}
		select {
		case <-c.gate:
		case <-time.After(10 * time.Second):
		}
		return nil, nil
	}))
	require.NoError(t, r.AddWorkflowN("terminalcost-fanout", func(octx *task.WorkflowContext) (any, error) {
		tasks := make([]task.Task, fanOut)
		for i := range tasks {
			tasks[i] = octx.CallActivity("terminalcost-gated")
		}
		for _, tk := range tasks {
			if err := tk.Await(nil); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}))
	require.NoError(t, r.AddActivityN("terminalcost-act", func(task.ActivityContext) (any, error) {
		return nil, nil
	}))
	require.NoError(t, r.AddWorkflowN("terminalcost-single", func(octx *task.WorkflowContext) (any, error) {
		return nil, octx.CallActivity("terminalcost-act").Await(nil)
	}))

	c.store.DelayBulkGets(fullLoadKey, 50*time.Millisecond)
}

// leftoverWakeups: every activity result arms one wake-up. When one turn
// consumes all the results and completes the workflow, the other wake-ups
// fire against the completed instance.
func (c *TerminalCost) leftoverWakeups(t *testing.T, ctx context.Context, bc *client.TaskHubGrpcClient) {
	t.Helper()
	const wfID = "terminalcost-fanout"

	// Counted from the start, so a load that races the completion log line
	// is not missed. A running instance keeps its state cached, so the loads
	// all come after completion.
	fullLoads := c.store.CountBulkGets(wfID + fullLoadKey)
	c.start(t, ctx, "terminalcost-fanout", wfID)

	completedLine := fmt.Sprintf("Workflow Actor '%s': workflow completed with status", wfID)
	require.True(t, c.log.EventuallyContains(t, completedLine, 30*time.Second, 5*time.Millisecond))

	ignored := fmt.Sprintf("Workflow actor '%s': ignoring run request for reminder 'new-event-", wfID)
	turns := fmt.Sprintf("processing work item: %s ", wfID)
	ignoredAtCompletion := c.log.Count(ignored)

	// A client asking for the result now queues on the actor lock behind
	// every wake-up already waiting there. Acks without a reload take about
	// 1 s for all of them under -race; reloads take at least 10 s.
	asked := time.Now()
	meta, err := bc.WaitForWorkflowCompletion(ctx, api.InstanceID(wfID))
	answered := time.Since(asked)
	t.Logf("completion wait answered after %s", answered)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.Less(t, answered, 8*time.Second, "the completed workflow answered its completion wait after %s", answered)

	// Each wake-up either drove a turn (the start turn aside) or found the
	// inbox empty.
	require.EventuallyWithT(t, func(co *assert.CollectT) {
		assert.GreaterOrEqual(co, c.log.Count(ignored)+c.log.Count(turns)-1, fanOut)
	}, 60*time.Second, 50*time.Millisecond)

	leftover := c.log.Count(ignored) - ignoredAtCompletion
	require.GreaterOrEqual(t, leftover, fanOut/2,
		"the completing turn did not consume most of the results, so too few wake-ups fired after completion to test")
	t.Logf("%d full loads for %d wake-ups after completion", fullLoads(), leftover)
	assert.LessOrEqual(t, fullLoads(), 3,
		"%d wake-ups of the completed workflow reloaded its history from the store", leftover)
}

// lateDuplicates: activities that re-ran (a host lost mid-fan-out) deliver
// their results again after the workflow completed. Each is dropped; the
// history read to judge the first serves the rest. This guards the cached
// dedup path, not the settled record: a duplicate is an actor method, which
// drops the record.
func (c *TerminalCost) lateDuplicates(t *testing.T, ctx context.Context, bc *client.TaskHubGrpcClient) {
	t.Helper()
	const (
		wfID       = "terminalcost-late"
		duplicates = 20
	)

	c.start(t, ctx, "terminalcost-single", wfID)
	meta, err := bc.WaitForWorkflowCompletion(ctx, api.InstanceID(wfID))
	require.NoError(t, err)
	require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())

	hist, err := bc.GetInstanceHistory(ctx, api.InstanceID(wfID))
	require.NoError(t, err)
	scheduledID := int32(-1)
	for _, he := range hist.GetEvents() {
		if he.GetTaskScheduled() != nil {
			scheduledID = he.GetEventId()
		}
	}
	require.NotEqual(t, int32(-1), scheduledID)

	dup, err := proto.Marshal(&protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskCompleted{TaskCompleted: &protos.TaskCompletedEvent{
			TaskScheduledId: scheduledID,
			Result:          wrapperspb.String(`"done"`),
		}},
	})
	require.NoError(t, err)
	dropped := fmt.Sprintf("Workflow actor '%s': dropping completion", wfID)

	fullLoads := c.store.CountBulkGets(wfID + fullLoadKey)
	ic := c.workflow.InternalGRPCClient(t, ctx)
	for range duplicates {
		_, err = ic.CallActor(ctx, internalv1pb.NewInternalInvokeRequest("AddWorkflowEvent").
			WithActor(c.workflow.WorkflowActorType(0), wfID).
			WithData(dup),
		)
		require.NoError(t, err)
	}
	require.EventuallyWithT(t, func(co *assert.CollectT) {
		assert.Equal(co, duplicates, c.log.Count(dropped))
	}, 10*time.Second, 10*time.Millisecond)

	// One read per actor residency: the completion wait deactivates the
	// actor, and that deactivation may land between two duplicates.
	assert.LessOrEqual(t, fullLoads(), 2,
		"%d late duplicates of the completed workflow reloaded its history from the store", duplicates)
}

func (c *TerminalCost) start(t *testing.T, ctx context.Context, name, wfID string) {
	t.Helper()
	_, err := c.workflow.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      name,
		InstanceId:        wfID,
	})
	require.NoError(t, err)
}
