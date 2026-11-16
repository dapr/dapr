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

package activityv2

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(busyparent))
}

// busyparent: activity results reach a parent workflow whose turn lock is
// held. In a large fan-out this is the normal state: the turn that schedules
// the activities, and every completion queued before a result, hold the same
// lock. A wait for that lock is backpressure, not a lost delivery: the parent
// admits each result when the lock is free, and each body runs one time. A
// result publish bounded at 30 s gave up while the parent was busy, the
// execution settled as failed, the run-activity reminder ran the body again,
// and the re-run queued on the same lock, so a large fan-out ran most bodies
// several times and slowed down as it grew.
type busyparent struct {
	workflow *workflow.Workflow
}

const (
	busyparentActivities = 5
	// busyparentHold is how long the parent's held turn holds its turn
	// lock once the publishes of the other four results wait for it. The
	// publish has no deadline, so the hold only has to outlast the queueing.
	// Test_runOwned_busyParentIsNotAFailedPublish checks that the publish
	// context has no deadline.
	busyparentHold = time.Second
	// busyparentGate bounds each wait on the order this test forces.
	busyparentGate = 10 * time.Second
)

func (b *busyparent) Setup(t *testing.T) []framework.Option {
	b.workflow = workflow.New(t)
	return []framework.Option{framework.WithProcesses(b.workflow)}
}

func (b *busyparent) Run(t *testing.T, ctx context.Context) {
	b.workflow.WaitUntilRunning(t, ctx)

	var (
		executions [busyparentActivities]atomic.Int64
		// held closes when the turn that the result of activity 0 drives
		// starts. Activities 1-4 return only after that, so their results
		// arrive while that turn holds the lock.
		held     = make(chan struct{})
		heldOnce sync.Once
		// heldWithResults is set when the held turn saw the publishes of all
		// four results wait for the lock before it started to hold it.
		heldWithResults atomic.Bool
	)

	// Calls that wait for the parent's turn lock. The held turn holds the
	// lock, so the publishes of activities 1-4 queue here.
	waiting := func() float64 {
		return b.workflow.Dapr().Metrics(t, ctx).SumWithLabels("dapr_runtime_actor_pending_actor_calls", "actor_type:"+b.workflow.WorkflowActorType(0))
	}

	require.NoError(t, b.workflow.Registry().AddWorkflowN("BusyParent", func(c *task.WorkflowContext) (any, error) {
		tasks := make([]task.Task, busyparentActivities)
		for i := range busyparentActivities {
			tasks[i] = c.CallActivity("Slow", task.WithActivityInput(i))
		}
		// The hold is tied to the turn that the result of activity 0
		// drives, not to an invocation count, so a first turn that is
		// delivered again does not start it.
		if err := tasks[0].Await(nil); err != nil {
			return nil, err
		}
		heldOnce.Do(func() {
			close(held)
			deadline := time.Now().Add(busyparentGate)
			for waiting() < busyparentActivities-1 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond * 10)
			}
			heldWithResults.Store(waiting() >= busyparentActivities-1)
			time.Sleep(busyparentHold)
		})
		errs := make([]error, 0, len(tasks)-1)
		for _, tk := range tasks[1:] {
			errs = append(errs, tk.Await(nil))
		}
		return nil, errors.Join(errs...)
	}))
	require.NoError(t, b.workflow.Registry().AddActivityN("Slow", func(c task.ActivityContext) (any, error) {
		var i int
		if err := c.GetInput(&i); err != nil {
			return nil, err
		}
		executions[i].Add(1)
		if i == 0 {
			// The first result drives the held turn.
			return nil, nil
		}
		select {
		case <-held:
		case <-time.After(busyparentGate):
			return nil, errors.New("the parent's held turn did not start")
		}
		return nil, nil
	}))

	cl := b.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "BusyParent")
	require.NoError(t, err)

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	require.True(t, heldWithResults.Load(),
		"the held turn must hold the parent's turn lock while the publishes of the four results wait for it")

	// Each result was in hand while it waited for the parent. Nothing was
	// lost and nothing moved, so each body must have run one time.
	for i := range busyparentActivities {
		assert.Equal(t, int64(1), executions[i].Load(),
			"activity %d ran more than one time: a publish that waited on a busy parent was settled as a failure", i)
	}
	assert.Never(t, func() bool {
		for i := range busyparentActivities {
			if executions[i].Load() > 1 {
				return true
			}
		}
		return false
	}, time.Millisecond*500, time.Millisecond*10)
}
