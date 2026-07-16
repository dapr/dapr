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
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(multidaprd))
}

type multidaprd struct {
	workflow *workflow.Workflow
}

func (m *multidaprd) Setup(t *testing.T) []framework.Option {
	m.workflow = workflow.New(t, workflow.WithDaprds(2))
	return []framework.Option{
		framework.WithProcesses(m.workflow),
	}
}

func (m *multidaprd) Run(t *testing.T, ctx context.Context) {
	m.workflow.WaitUntilRunning(t, ctx)

	const daprds = 2
	var calls [daprds]atomic.Int32
	holds := make([]chan struct{}, daprds)
	for i := range holds {
		holds[i] = make(chan struct{})
	}

	for i := range daprds {
		idx := i
		require.NoError(t, m.workflow.RegistryN(idx).AddWorkflowN("wf",
			func(ctx *task.WorkflowContext) (any, error) {
				return nil, ctx.CallActivity("act").Await(nil)
			}))
		require.NoError(t, m.workflow.RegistryN(idx).AddActivityN("act",
			func(ctx task.ActivityContext) (any, error) {
				calls[idx].Add(1)
				select {
				case <-holds[idx]:
				case <-ctx.Context().Done():
				}
				return nil, nil
			}))
	}

	ids := make([]api.InstanceID, daprds)
	for i := range daprds {
		cl := m.workflow.BackendClientN(t, ctx, i)
		id, err := cl.ScheduleNewWorkflow(ctx, "wf",
			api.WithInstanceID(api.InstanceID(fmt.Sprintf("md-%d", i))))
		require.NoError(t, err)
		ids[i] = id
	}

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		for i := range daprds {
			assert.Equal(c, int32(1), calls[i].Load(), "daprd %d", i)
		}
	}, 30*time.Second, 10*time.Millisecond)

	sched := m.workflow.Scheduler()
	sched.RestartGraceful(t, ctx)
	sched.WaitUntilRunning(t, ctx)
	sched.WaitUntilLeadership(t, ctx, 1)

	require.Never(t, func() bool {
		for i := range daprds {
			if calls[i].Load() > 1 {
				return true
			}
		}
		return false
	}, 5*time.Second, 100*time.Millisecond)

	for i := range holds {
		close(holds[i])
	}

	for i := range daprds {
		cl := m.workflow.BackendClientN(t, ctx, i)
		meta, err := cl.WaitForWorkflowCompletion(ctx, ids[i])
		require.NoError(t, err, "daprd %d", i)
		require.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String(), "daprd %d", i)
	}
	for i := range daprds {
		assert.Equal(t, int32(1), calls[i].Load(), "daprd %d", i)
	}
}
