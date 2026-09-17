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
	suite.Register(new(scaleup))
}

// scaleup asserts the autoscaling story: activities waiting in the scheduler
// are handed to replicas that join later, one per free slot, without any
// re-dispatch by the orchestrator.
type scaleup struct {
	workflow *workflow.Workflow
}

const scaleupAppID = "pull-scaleup"

func (s *scaleup) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, pullConfig("pullscaleup", 1)), daprd.WithAppID(scaleupAppID)),
	)
	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *scaleup) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	const n = 3
	var started [n]atomic.Int64
	releaseCh := make(chan struct{})
	registry := func(i int) *task.TaskRegistry {
		r := task.NewTaskRegistry()
		require.NoError(t, r.AddWorkflowN("fanout", func(ctx *task.WorkflowContext) (any, error) {
			tasks := make([]task.Task, n)
			for j := range n {
				tasks[j] = ctx.CallActivity("slow")
			}
			for _, tk := range tasks {
				if err := tk.Await(nil); err != nil {
					return nil, err
				}
			}
			return nil, nil
		}))
		require.NoError(t, r.AddActivityN("slow", func(ctx task.ActivityContext) (any, error) {
			started[i].Add(1)
			<-releaseCh
			return nil, nil
		}))
		return r
	}

	client := connectWorker(t, ctx, s.workflow.Dapr(), registry(0))
	id, err := client.ScheduleNewWorkflow(ctx, "fanout", api.WithStartTime(time.Now()))
	require.NoError(t, err)

	backlog := func() float64 {
		return s.workflow.Scheduler().Metrics(t, ctx).SumWithLabels(
			"dapr_scheduler_workflow_activity_backlog", "app_id:"+scaleupAppID)
	}

	// One slot in the fleet: one runs, two wait in the scheduler.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), started[0].Load())
		assert.InDelta(c, 2.0, backlog(), 0)
	}, time.Second*20, time.Millisecond*10)

	// A second replica joins: it takes one of the waiting activities as soon
	// as it hosts the activity actor type, while the first stays blocked.
	second := joinDaprd(t, s.workflow, scaleupAppID, pullConfig("pullscaleup", 1))
	second.Run(t, ctx)
	t.Cleanup(func() { second.Cleanup(t) })
	second.WaitUntilRunning(t, ctx)
	connectWorker(t, ctx, second, registry(1))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), started[1].Load())
		assert.InDelta(c, 1.0, backlog(), 0)
	}, time.Second*20, time.Millisecond*10)
	assert.Equal(t, int64(1), started[0].Load(), "the first replica has no free slot and must not be handed more work")

	// A third replica drains the backlog.
	third := joinDaprd(t, s.workflow, scaleupAppID, pullConfig("pullscaleup", 1))
	third.Run(t, ctx)
	t.Cleanup(func() { third.Cleanup(t) })
	third.WaitUntilRunning(t, ctx)
	connectWorker(t, ctx, third, registry(2))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), started[2].Load())
		assert.InDelta(c, 0.0, backlog(), 0)
	}, time.Second*20, time.Millisecond*10)

	close(releaseCh)
	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	for i := range n {
		assert.Equal(t, int64(1), started[i].Load(), "replica %d ran exactly one activity", i)
	}
}
