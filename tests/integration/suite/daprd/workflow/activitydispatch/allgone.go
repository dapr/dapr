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

package activitydispatch

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(allgone))
}

// allgone asserts durability when every replica disappears with work running
// and work waiting: a fresh replica that joins later receives both the
// activity that was in flight and the one that was parked, and the workflow
// completes.
type allgone struct {
	workflow *workflow.Workflow
}

const allgoneAppID = "pull-allgone"

func (a *allgone) Setup(t *testing.T) []framework.Option {
	a.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, pullConfig("pullallgone", 1)), daprd.WithAppID(allgoneAppID)),
	)
	return []framework.Option{
		framework.WithProcesses(a.workflow),
	}
}

func (a *allgone) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	var startedOld, startedNew atomic.Int64
	releaseCh := make(chan struct{})
	registry := func(counter *atomic.Int64) *task.TaskRegistry {
		r := task.NewTaskRegistry()
		require.NoError(t, r.AddWorkflowN("pair", func(ctx *task.WorkflowContext) (any, error) {
			t1 := ctx.CallActivity("slow")
			t2 := ctx.CallActivity("slow")
			if err := t1.Await(nil); err != nil {
				return nil, err
			}
			return nil, t2.Await(nil)
		}))
		require.NoError(t, r.AddActivityN("slow", func(ctx task.ActivityContext) (any, error) {
			counter.Add(1)
			<-releaseCh
			return nil, nil
		}))
		return r
	}

	oldClient := connectWorker(t, ctx, a.workflow.Dapr(), registry(&startedOld))
	id, err := oldClient.ScheduleNewWorkflow(ctx, "pair", api.WithStartTime(time.Now()))
	require.NoError(t, err)

	backlog := func() float64 {
		return a.workflow.Scheduler().Metrics(t, ctx).SumWithLabels(
			"dapr_scheduler_workflow_activity_backlog", "app_id:"+allgoneAppID)
	}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), startedOld.Load())
		assert.InDelta(c, 1.0, backlog(), 0)
	}, time.Second*20, time.Millisecond*10)

	// The only replica dies with one activity running and one waiting. The
	// waiting trigger goes back to the cron; the in-flight one is redelivered.
	a.workflow.Dapr().Kill(t)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.InDelta(c, 0.0, backlog(), 0)
	}, time.Second*20, time.Millisecond*10)

	fresh := joinDaprd(t, a.workflow, allgoneAppID, pullConfig("pullallgone", 1))
	fresh.Run(t, ctx)
	t.Cleanup(func() { fresh.Cleanup(t) })
	fresh.WaitUntilRunning(t, ctx)
	newClient := connectWorker(t, ctx, fresh, registry(&startedNew))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), startedNew.Load())
	}, time.Second*60, time.Millisecond*10)

	close(releaseCh)
	_, err = newClient.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, int64(2), startedNew.Load(), "both activities ran on the replacement replica")
}
