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
	"sync"
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
	suite.Register(new(fairness))
}

// fairness asserts instance round robin in the pull backlog: a workflow
// instance with a large fan-out does not starve a later instance with a single
// activity, which is served before the fan-out's second remaining task.
type fairness struct {
	workflow *workflow.Workflow
}

func (f *fairness) Setup(t *testing.T) []framework.Option {
	f.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, pullConfig("pullfairness", 1)), daprd.WithAppID("pull-fairness")),
	)
	return []framework.Option{
		framework.WithProcesses(f.workflow),
	}
}

func (f *fairness) Run(t *testing.T, ctx context.Context) {
	f.workflow.WaitUntilRunning(t, ctx)

	var mu sync.Mutex
	var started []string
	releaseCh := make(chan struct{})
	startedLen := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(started)
	}

	f.workflow.Registry().AddWorkflowN("fanout", func(ctx *task.WorkflowContext) (any, error) {
		var tag string
		if err := ctx.GetInput(&tag); err != nil {
			return nil, err
		}
		tasks := make([]task.Task, 4)
		for j := range tasks {
			tasks[j] = ctx.CallActivity("slow", task.WithActivityInput(tag))
		}
		for _, tk := range tasks {
			if err := tk.Await(nil); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	f.workflow.Registry().AddWorkflowN("single", func(ctx *task.WorkflowContext) (any, error) {
		var tag string
		if err := ctx.GetInput(&tag); err != nil {
			return nil, err
		}
		return nil, ctx.CallActivity("slow", task.WithActivityInput(tag)).Await(nil)
	})
	f.workflow.Registry().AddActivityN("slow", func(ctx task.ActivityContext) (any, error) {
		var tag string
		if err := ctx.GetInput(&tag); err != nil {
			return nil, err
		}
		mu.Lock()
		started = append(started, tag)
		mu.Unlock()
		<-releaseCh
		return nil, nil
	})

	client := f.workflow.BackendClient(t, ctx)

	// Instance A fans out four activities into a single slot: one runs, three
	// wait. Then instance B schedules one activity behind them.
	idA, err := client.ScheduleNewWorkflow(ctx, "fanout", api.WithInput("A"), api.WithStartTime(time.Now()))
	require.NoError(t, err)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 1, startedLen())
	}, time.Second*20, time.Millisecond*10)

	idB, err := client.ScheduleNewWorkflow(ctx, "single", api.WithInput("B"), api.WithStartTime(time.Now()))
	require.NoError(t, err)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.InDelta(c, 4.0, f.workflow.Scheduler().Metrics(t, ctx).SumWithLabels(
			"dapr_scheduler_workflow_activity_backlog", "app_id:pull-fairness"), 0)
	}, time.Second*20, time.Millisecond*10)

	// Each release frees the slot. Round robin across instances serves A's
	// next task, then B, then A again; FIFO would have run all of A first.
	for want := 2; want <= 5; want++ {
		releaseCh <- struct{}{}
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Equal(c, want, startedLen())
		}, time.Second*20, time.Millisecond*10)
	}

	mu.Lock()
	order := append([]string(nil), started...)
	mu.Unlock()
	assert.Equal(t, []string{"A", "A", "B", "A", "A"}, order)

	close(releaseCh)
	_, err = client.WaitForWorkflowCompletion(ctx, idA)
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, idB)
	require.NoError(t, err)
}
