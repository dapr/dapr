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

package propagation

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	procworkflow "github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(multiapplineage))
}

// multiapplineage tests multi-hop history propagation across 3 different apps:
// App0 -> App1 -> App2, w/ PropagateLineage at each hop. App2 should see
// the full ancestor chain: events from both App0 and App1, forwarded
// across two app boundaries
type multiapplineage struct {
	workflow *procworkflow.Workflow

	app2HistoryReceived atomic.Bool
	app2TotalEvents     atomic.Int64
	app2App0ActCount    atomic.Int64
	app2App1ActCount    atomic.Int64
}

func (m *multiapplineage) Setup(t *testing.T) []framework.Option {
	m.workflow = procworkflow.New(t,
		procworkflow.WithDaprds(3),
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{
		framework.WithProcesses(m.workflow),
	}
}

func (m *multiapplineage) Run(t *testing.T, ctx context.Context) {
	m.workflow.WaitUntilRunning(t, ctx)

	app0Reg := m.workflow.Registry()
	app1Reg := m.workflow.RegistryN(1)
	app2Reg := m.workflow.RegistryN(2)

	// App0: root workflow, calls app0Act, then creates middleWf on App1
	app0Reg.AddActivityN("app0Act", func(ctx task.ActivityContext) (any, error) {
		return "app0-done", nil
	})
	app0Reg.AddWorkflowN("rootWf", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("app0Act").Await(nil); err != nil {
			return nil, err
		}

		var result string
		if err := ctx.CallChildWorkflow("middleWf",
			task.WithChildWorkflowAppID(m.workflow.DaprN(1).AppID()),
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&result); err != nil {
			return nil, err
		}
		return result, nil
	})

	// App1: middle wf, calls app1Act, then creates leafWf on App2
	// Uses Lineage so App0's history is forwarded onward to App2
	app1Reg.AddActivityN("app1Act", func(ctx task.ActivityContext) (any, error) {
		return "app1-done", nil
	})
	app1Reg.AddWorkflowN("middleWf", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("app1Act").Await(nil); err != nil {
			return nil, err
		}

		var result string
		if err := ctx.CallChildWorkflow("leafWf",
			task.WithChildWorkflowAppID(m.workflow.DaprN(2).AppID()),
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&result); err != nil {
			return nil, err
		}
		return result, nil
	})

	// App2: leaf workflow, reads full propagated chain from both App0 & App1
	app2Reg.AddWorkflowN("leafWf", func(ctx *task.WorkflowContext) (any, error) {
		ph := ctx.GetPropagatedHistory()
		if ph == nil {
			return "no history", nil
		}

		m.app2HistoryReceived.Store(true)
		m.app2TotalEvents.Store(int64(len(ph.Events())))
		for _, e := range ph.Events() {
			if ts := e.GetTaskScheduled(); ts != nil {
				switch ts.GetName() {
				case "app0Act": //nolint:goconst
					m.app2App0ActCount.Add(1)
				case "app1Act":
					m.app2App1ActCount.Add(1)
				}
			}
		}

		return "done", nil
	})

	client0 := m.workflow.BackendClient(t, ctx)
	m.workflow.BackendClientN(t, ctx, 1)
	m.workflow.BackendClientN(t, ctx, 2)

	id, err := client0.ScheduleNewWorkflow(ctx, "rootWf")
	require.NoError(t, err)

	metadata, err := client0.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))

	assert.True(t, m.app2HistoryReceived.Load(), "App2 should have received propagated history")

	// Expected 12 events — App0's history (forwarded through App1) + App1's own:
	//
	//   Events [0-5]: App0's history (ancestor, forwarded through App1 via lineage)
	//     [0] WorkflowStarted                — App0 begins
	//     [1] ExecutionStarted                — App0's metadata
	//     [2] TaskScheduled("app0Act")        — App0 schedules app0Act
	//     [3] WorkflowStarted                — App0 replays after app0Act completes
	//     [4] TaskCompleted                   — app0Act result
	//     [5] ChildWorkflowInstanceCreated    — App0 creates middleWf on App1
	//
	//   Events [6-11]: App1's own history
	//     [6] WorkflowStarted                — App1 begins
	//     [7] ExecutionStarted                — App1's metadata
	//     [8] TaskScheduled("app1Act")        — App1 schedules app1Act
	//     [9] WorkflowStarted                — App1 replays after app1Act completes
	//     [10] TaskCompleted                  — app1Act result
	//     [11] ChildWorkflowInstanceCreated   — App1 creates leafWf on App2
	assert.Equal(t, int64(12), m.app2TotalEvents.Load(), "App2 should receive 12 events: 6 from App0 (ancestor) + 6 from App1 (own)")
	assert.Equal(t, int64(1), m.app2App0ActCount.Load(), "App2 should see app0Act from App0 (via lineage through App1)")
	assert.Equal(t, int64(1), m.app2App1ActCount.Load(), "App2 should see app1Act from App1's own history")
}
