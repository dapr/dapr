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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(busyparentshutdown))
}

// busyparentshutdown: daprd shuts down while activity results wait for a
// busy parent workflow. Shutdown cancels the waiting result publishes at once.
// With WorkflowsRemoteActivityReminder on (the default), each activity then
// hands its result to a durable activity-result reminder, inside the publish
// shutdown grace, so no result is lost. crossapp/resultreminder/restart
// covers how the parent admits the results from these reminders after a
// restart. busyparentabandon covers a publish that cannot hand off its result
// inside the grace.
type busyparentshutdown struct {
	workflow *workflow.Workflow
}

const (
	busyparentShutdownActivities = 5
	// busyparentShutdownGate bounds each wait on the order this test forces.
	busyparentShutdownGate = 20 * time.Second
)

func (b *busyparentshutdown) Setup(t *testing.T) []framework.Option {
	b.workflow = workflow.New(t)
	return []framework.Option{framework.WithProcesses(b.workflow)}
}

func (b *busyparentshutdown) Run(t *testing.T, ctx context.Context) {
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
	require.NoError(t, reg.AddWorkflowN("BusyParentShutdown", func(c *dworkflow.WorkflowContext) (any, error) {
		tasks := make([]dworkflow.Task, busyparentShutdownActivities)
		for i := range busyparentShutdownActivities {
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
			case <-time.After(busyparentShutdownGate * 2):
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
		case <-time.After(busyparentShutdownGate):
			return nil, errors.New("the parent's held turn did not start")
		}
		return nil, nil
	}))

	wctx, wcancel := context.WithCancel(ctx)
	t.Cleanup(wcancel)
	client := dworkflow.NewClientWithLogger(b.workflow.Dapr().GRPCConn(t, wctx), logger.New(t))
	require.NoError(t, client.StartWorker(wctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "BusyParentShutdown")
	require.NoError(t, err)

	// Calls that wait for the parent's turn lock. The held turn holds the
	// lock, so the publishes of activities 1-4 queue here.
	waiting := func() float64 {
		return b.workflow.Dapr().Metrics(t, ctx).SumWithLabels("dapr_runtime_actor_pending_actor_calls", "actor_type:"+b.workflow.WorkflowActorType(0))
	}
	require.Eventually(t, func() bool {
		return holding.Load() && waiting() >= busyparentShutdownActivities-1
	}, busyparentShutdownGate, time.Millisecond*10,
		"the results of activities 1-4 must wait for the parent's turn lock")

	// daprd shuts down while the results wait for the parent. The worker
	// stays connected until daprd is gone, so the held turn does not end
	// first and admit the results.
	start := time.Now()
	b.workflow.Dapr().Cleanup(t)
	stopped := time.Since(start)
	wcancel()
	close(release)

	assert.Less(t, stopped, busyparentShutdownGate,
		"shutdown must not wait for the parent: it cancels the waiting publishes at once")

	// Each waiting result moved to a durable activity-result reminder. Under
	// fast path, the publish of activity 0 waits too: the parent holds that
	// result in memory until the held turn commits, so the result moves to
	// a reminder as well.
	expResults := busyparentShutdownActivities - 1
	if b.workflow.FastPath() {
		expResults = busyparentShutdownActivities
	}
	resultPrefix := "dapr/jobs/actorreminder||default||" + b.workflow.WorkflowActorType(0) + "||" + id + "||activity-result-"
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		var results int
		for _, key := range b.workflow.Scheduler().ListAllKeys(t, ctx, "dapr/jobs/actorreminder") {
			if strings.HasPrefix(key, resultPrefix) {
				results++
			}
		}
		assert.Equal(c, expResults, results)
	}, time.Second*10, time.Millisecond*10)
}
