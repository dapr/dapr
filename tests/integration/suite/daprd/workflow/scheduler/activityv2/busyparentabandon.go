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
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(busyparentabandon))
}

// busyparentabandon: daprd shuts down while activity results wait for a busy
// parent workflow, and the scheduler is down. So no result can move to an
// activity-result reminder: each create retries until the publish shutdown
// grace ends the publish. The publish then gives up its result with a
// warning, and the shutdown does not wait longer than the grace. The grace is
// the graceful shutdown duration, which the test sets to 1 s.
// busyparentshutdown covers the handoff to the reminders when the scheduler
// is up.
type busyparentabandon struct {
	workflow *workflow.Workflow
	logline  *logline.LogLine
}

const (
	busyparentAbandonActivities = 5
	busyparentAbandonGrace      = time.Second
	// busyparentAbandonGate bounds each wait on the order this test forces.
	busyparentAbandonGate = 20 * time.Second
)

func (b *busyparentabandon) Setup(t *testing.T) []framework.Option {
	b.logline = logline.New(t, logline.WithCaptureAll())
	b.workflow = workflow.New(t,
		// The test stops the scheduler, so actor placement must not depend
		// on it.
		workflow.WithPlacementService(),
		workflow.WithDaprdOptions(0,
			daprd.WithDaprGracefulShutdownSeconds(int(busyparentAbandonGrace/time.Second)),
			daprd.WithExecOptions(
				exec.WithStdout(b.logline.Stdout()),
				exec.WithStderr(b.logline.Stderr()),
			),
		),
	)
	return []framework.Option{framework.WithProcesses(b.logline, b.workflow)}
}

func (b *busyparentabandon) Run(t *testing.T, ctx context.Context) {
	b.workflow.WaitUntilRunning(t, ctx)

	var (
		// held closes when the turn that the result of activity 0 drives
		// starts. Activities 1-4 return only after that, so their results
		// arrive while that turn holds the parent's turn lock.
		held     = make(chan struct{})
		heldOnce sync.Once
		// holding is set while the held turn holds the lock.
		holding atomic.Bool
		// release ends the hold. The test closes it after daprd stopped, so
		// the worker goroutine of the held turn can return.
		release = make(chan struct{})
	)

	reg := dworkflow.NewRegistry()
	require.NoError(t, reg.AddWorkflowN("BusyParentAbandon", func(c *dworkflow.WorkflowContext) (any, error) {
		tasks := make([]dworkflow.Task, busyparentAbandonActivities)
		for i := range busyparentAbandonActivities {
			tasks[i] = c.CallActivity("Slow", dworkflow.WithActivityInput(i))
		}
		// The hold is tied to the turn that the result of activity 0
		// drives, not to an invocation count, so a first turn that is
		// delivered again does not start it.
		if err := tasks[0].Await(nil); err != nil {
			return nil, err
		}
		heldOnce.Do(func() {
			close(held)
			holding.Store(true)
			select {
			case <-release:
			case <-time.After(busyparentAbandonGate * 2):
			}
		})
		errs := make([]error, 0, len(tasks)-1)
		for _, tk := range tasks[1:] {
			errs = append(errs, tk.Await(nil))
		}
		return nil, errors.Join(errs...)
	}))
	require.NoError(t, reg.AddActivityN("Slow", func(c dworkflow.ActivityContext) (any, error) {
		var i int
		if err := c.GetInput(&i); err != nil {
			return nil, err
		}
		if i == 0 {
			// The first result drives the held turn.
			return nil, nil
		}
		select {
		case <-held:
		case <-time.After(busyparentAbandonGate):
			return nil, errors.New("the parent's held turn did not start")
		}
		return nil, nil
	}))

	wctx, wcancel := context.WithCancel(ctx)
	t.Cleanup(wcancel)
	client := dworkflow.NewClientWithLogger(b.workflow.Dapr().GRPCConn(t, wctx), logger.New(t))
	require.NoError(t, client.StartWorker(wctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "BusyParentAbandon")
	require.NoError(t, err)

	// Calls that wait for the parent's turn lock. The held turn holds the
	// lock, so the publishes of activities 1-4 queue here.
	waiting := func() float64 {
		return b.workflow.Dapr().Metrics(t, ctx).SumWithLabels("dapr_runtime_actor_pending_actor_calls", "actor_type:"+b.workflow.WorkflowActorType(0))
	}
	require.Eventually(t, func() bool {
		return holding.Load() && waiting() >= busyparentAbandonActivities-1
	}, busyparentAbandonGate, time.Millisecond*10,
		"the results of activities 1-4 must wait for the parent's turn lock")

	b.workflow.Scheduler().Kill(t)

	// daprd shuts down while the results wait for the parent. The worker
	// stays connected until daprd is gone, so the held turn does not end
	// first and admit the results.
	start := time.Now()
	b.workflow.Dapr().Cleanup(t)
	stopped := time.Since(start)
	wcancel()
	close(release)

	// The shutdown waits for the publishes until the grace ends them, and
	// not longer.
	assert.GreaterOrEqual(t, stopped, busyparentAbandonGrace,
		"shutdown must wait for the publishes for the grace")
	assert.Less(t, stopped, busyparentAbandonGate,
		"shutdown must not wait for the publishes for longer than the grace")

	// Each publish that waited gave up its result. Under fast path, the
	// publish of activity 0 waits too: the parent holds that result in
	// memory until the held turn commits.
	expAbandoned := busyparentAbandonActivities - 1
	if b.workflow.FastPath() {
		expAbandoned = busyparentAbandonActivities
	}
	abandoned := "abandoning the result publish to workflow '" + id + "' at shutdown"
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, expAbandoned, b.logline.Count(abandoned))
	}, time.Second*10, time.Millisecond*10)
}
