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
	suite.Register(new(multiapp))
}

// multiapp tests that propagated history flows correctly across app
// boundaries. App0 runs the parent wf which calls a local activity, then
// invokes a child wf on App1 w/ PropagateLineage. App1's child
// wf should receive the parent's full history from App0
type multiapp struct {
	workflow *procworkflow.Workflow

	childHistoryReceived atomic.Bool
	childTotalEvents     atomic.Int64
	childTaskCount       atomic.Int64
	childHasLocalAct     atomic.Bool
	childHasRemoteAct    atomic.Bool
}

func (m *multiapp) Setup(t *testing.T) []framework.Option {
	m.workflow = procworkflow.New(t,
		procworkflow.WithDaprds(2),
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{
		framework.WithProcesses(m.workflow),
	}
}

func (m *multiapp) Run(t *testing.T, ctx context.Context) {
	m.workflow.WaitUntilRunning(t, ctx)

	app0Reg := m.workflow.Registry()
	app1Reg := m.workflow.RegistryN(1)

	app0Reg.AddActivityN("localValidation", func(ctx task.ActivityContext) (any, error) {
		return "validated", nil
	})

	app0Reg.AddWorkflowN("parentWorkflow", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("localValidation", task.WithActivityInput("check")).Await(nil); err != nil {
			return nil, err
		}

		remoteAppID := m.workflow.DaprN(1).AppID()
		var childResult string
		if err := ctx.CallChildWorkflow("remoteChildWorkflow",
			task.WithChildWorkflowInput("from-app0"),
			task.WithChildWorkflowAppID(remoteAppID),
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&childResult); err != nil {
			return nil, err
		}

		return childResult, nil
	})

	app1Reg.AddWorkflowN("remoteChildWorkflow", func(ctx *task.WorkflowContext) (any, error) {
		ph := ctx.GetPropagatedHistory()
		if ph == nil {
			return statusNoHistory, nil
		}

		m.childHistoryReceived.Store(true)
		m.childTotalEvents.Store(int64(len(ph.Events())))
		for _, e := range ph.Events() {
			if ts := e.GetTaskScheduled(); ts != nil {
				m.childTaskCount.Add(1)
				if ts.GetName() == "localValidation" {
					m.childHasLocalAct.Store(true)
				}
			}
			if e.GetChildWorkflowInstanceCreated() != nil {
				m.childHasRemoteAct.Store(true)
			}
		}

		return statusDone, nil
	})

	client0 := m.workflow.BackendClient(t, ctx)
	m.workflow.BackendClientN(t, ctx, 1)

	id, err := client0.ScheduleNewWorkflow(ctx, "parentWorkflow", api.WithInput("test"))
	require.NoError(t, err)

	metadata, err := client0.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))

	assert.True(t, m.childHistoryReceived.Load(), "child on App1 should have received propagated history from App0")

	// Expected 6 events — App0's history crosses the app boundary via gRPC:
	//
	//   Batch 1 (App0 starts, schedules localValidation):
	//     [0] WorkflowStarted                — App0 orchestrator begins
	//     [1] ExecutionStarted                — App0 workflow metadata
	//     [2] TaskScheduled("localValidation") — App0 schedules local activity
	//
	//   Batch 2 (localValidation completes, App0 creates remote child):
	//     [3] WorkflowStarted                — App0
	//     [4] TaskCompleted                   — localValidation result
	//     [5] ChildWorkflowInstanceCreated    — App0 creates child on App1
	assert.Equal(t, int64(6), m.childTotalEvents.Load(), "child should receive exactly 6 events from App0")
	assert.Equal(t, int64(1), m.childTaskCount.Load(), "1 TaskScheduled: localValidation")
	assert.True(t, m.childHasLocalAct.Load(), "child should see App0's 'localValidation' in propagated history")
	assert.True(t, m.childHasRemoteAct.Load(), "child should see the ChildWorkflowInstanceCreated for itself")
}
