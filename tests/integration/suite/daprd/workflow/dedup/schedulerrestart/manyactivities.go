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

package schedulerrestart

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(manyactivities))
}

// manyactivities runs a workflow with multiple parallel activities, holds
// them all, kills+restarts the scheduler, and asserts no activity is
// re-executed.
type manyactivities struct {
	workflow *workflow.Workflow
}

func (m *manyactivities) Setup(t *testing.T) []framework.Option {
	m.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(m.workflow),
	}
}

func (m *manyactivities) Run(t *testing.T, ctx context.Context) {
	m.workflow.WaitUntilRunning(t, ctx)

	const n = 2
	calls := make([]atomic.Int32, n)
	holds := make([]chan struct{}, n)
	for i := range holds {
		holds[i] = make(chan struct{})
	}

	require.NoError(t, m.workflow.Registry().AddWorkflowN("wf",
		func(ctx *task.WorkflowContext) (any, error) {
			tasks := make([]task.Task, n)
			for i := range tasks {
				tasks[i] = ctx.CallActivity(fmt.Sprintf("act-%d", i))
			}
			for _, ta := range tasks {
				if err := ta.Await(nil); err != nil {
					return nil, err
				}
			}
			return nil, nil
		}))
	for i := range n {
		idx := i
		require.NoError(t, m.workflow.Registry().AddActivityN(fmt.Sprintf("act-%d", idx),
			func(ctx task.ActivityContext) (any, error) {
				calls[idx].Add(1)
				select {
				case <-holds[idx]:
				case <-ctx.Context().Done():
				}
				return nil, nil
			}))
	}

	cl := m.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "wf")
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		for i := range n {
			assert.Equal(c, int32(1), calls[i].Load(), "activity %d", i)
		}
	}, 20*time.Second, 10*time.Millisecond)

	sched := m.workflow.Scheduler()
	sched.RestartGraceful(t, ctx)
	sched.WaitUntilRunning(t, ctx)
	sched.WaitUntilLeadership(t, ctx, 1)

	require.Never(t, func() bool {
		for i := range n {
			if calls[i].Load() > 1 {
				return true
			}
		}
		return false
	}, 3*time.Second, 100*time.Millisecond, "no activity must be re-executed")

	for i := range holds {
		close(holds[i])
	}

	cl = m.workflow.BackendClient(t, ctx)
	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	for i := range n {
		assert.Equal(t, int32(1), calls[i].Load(), "activity %d", i)
	}
}
