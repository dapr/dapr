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
	suite.Register(new(schedulerrestart))
}

// schedulerrestart asserts that the pull backlog survives a scheduler
// restart: parked triggers are persisted jobs, so after the restart they are
// dispatched again as slots free up and the workflow completes.
type schedulerrestart struct {
	workflow *workflow.Workflow
}

const schedulerrestartAppID = "pull-schedrestart"

func (s *schedulerrestart) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, pullConfig("pullschedrestart", 1)), daprd.WithAppID(schedulerrestartAppID)),
	)
	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *schedulerrestart) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	var started atomic.Int64
	releaseCh := make(chan struct{})
	s.workflow.Registry().AddWorkflowN("fanout", func(ctx *task.WorkflowContext) (any, error) {
		tasks := []task.Task{ctx.CallActivity("slow"), ctx.CallActivity("slow"), ctx.CallActivity("slow")}
		for _, tk := range tasks {
			if err := tk.Await(nil); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	s.workflow.Registry().AddActivityN("slow", func(ctx task.ActivityContext) (any, error) {
		started.Add(1)
		<-releaseCh
		return nil, nil
	})

	client := s.workflow.BackendClient(t, ctx)
	id, err := client.ScheduleNewWorkflow(ctx, "fanout", api.WithStartTime(time.Now()))
	require.NoError(t, err)

	backlog := func() float64 {
		return s.workflow.Scheduler().Metrics(t, ctx).SumWithLabels(
			"dapr_scheduler_workflow_activity_backlog", "app_id:"+schedulerrestartAppID)
	}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), started.Load())
		assert.InDelta(c, 2.0, backlog(), 0)
	}, time.Second*20, time.Millisecond*10)

	s.workflow.Scheduler().RestartGraceful(t, ctx)
	s.workflow.Scheduler().WaitUntilRunning(t, ctx)

	// The two parked triggers are re-triggered from etcd and park again
	// behind the still-running activity's slot.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.InDelta(c, 2.0, backlog(), 0)
	}, time.Second*60, time.Millisecond*10)
	assert.Equal(t, int64(1), started.Load(), "the running activity must not be duplicated by the restart while its sidecar is alive")

	releaseCh <- struct{}{}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(2), started.Load())
	}, time.Second*60, time.Millisecond*10)

	close(releaseCh)
	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
}
