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
	suite.Register(new(chunksparallel))
}

// chunksparallel tests that parallel activities in 1 wf produce a
// single chunk (same app) & all scheduled/completed activities survive
// propagation. App0 runs parentWf which fans out 3 activities, awaits all,
// then calls childWf on App1 w/ PropagateLineage
type chunksparallel struct {
	workflow *procworkflow.Workflow

	childHistoryReceived atomic.Bool
	childTotalEvents     atomic.Int32
	childChunkCount      atomic.Int32

	childChunks         atomic.Value
	childAppIDs         atomic.Value
	childActNames       atomic.Value
	childCompletedNames atomic.Value
}

func (c *chunksparallel) Setup(t *testing.T) []framework.Option {
	c.workflow = procworkflow.New(t,
		procworkflow.WithDaprds(2),
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *chunksparallel) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	app0Reg := c.workflow.Registry()
	app1Reg := c.workflow.RegistryN(1)
	app0AppID := c.workflow.DaprN(0).AppID()
	app0Reg.AddActivityN("parallelAct1", func(ctx task.ActivityContext) (any, error) {
		return "result1", nil
	})
	app0Reg.AddActivityN("parallelAct2", func(ctx task.ActivityContext) (any, error) {
		return "result2", nil
	})
	app0Reg.AddActivityN("parallelAct3", func(ctx task.ActivityContext) (any, error) {
		return "result3", nil
	})

	app0Reg.AddWorkflowN("parentWf", func(ctx *task.WorkflowContext) (any, error) {
		// Schedule 3 activities in parallel (fan-out)
		t1 := ctx.CallActivity("parallelAct1")
		t2 := ctx.CallActivity("parallelAct2")
		t3 := ctx.CallActivity("parallelAct3")

		// Await all (fan-in)
		if err := t1.Await(nil); err != nil {
			return nil, err
		}
		if err := t2.Await(nil); err != nil {
			return nil, err
		}
		if err := t3.Await(nil); err != nil {
			return nil, err
		}

		// Propagate full history to child on App1
		var result string
		if err := ctx.CallChildWorkflow("childWf",
			task.WithChildWorkflowAppID(c.workflow.DaprN(1).AppID()),
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&result); err != nil {
			return nil, err
		}
		return result, nil
	})

	// App1: verify chunks contain all parallel activity events
	app1Reg.AddWorkflowN("childWf", func(ctx *task.WorkflowContext) (any, error) {
		ph := ctx.GetPropagatedHistory()
		if ph == nil {
			return "no history", nil
		}

		c.childHistoryReceived.Store(true)
		c.childTotalEvents.Store(int32(len(ph.Events())))
		workflows := ph.GetWorkflows()
		c.childChunkCount.Store(int32(len(workflows)))

		actNames := make(map[string]bool)
		completedNames := make(map[string]bool)
		scheduledByExecID := make(map[string]string)

		for _, e := range ph.Events() {
			if ts := e.GetTaskScheduled(); ts != nil {
				actNames[ts.GetName()] = true
				scheduledByExecID[ts.GetTaskExecutionId()] = ts.GetName()
			}
			if tc := e.GetTaskCompleted(); tc != nil {
				if name, ok := scheduledByExecID[tc.GetTaskExecutionId()]; ok {
					completedNames[name] = true
				}
			}
		}

		c.childChunks.Store(workflows)
		c.childAppIDs.Store(ph.GetAppIDs())
		c.childActNames.Store(actNames)
		c.childCompletedNames.Store(completedNames)

		return "done", nil
	})

	client0 := c.workflow.BackendClient(t, ctx)
	c.workflow.BackendClientN(t, ctx, 1)

	id, err := client0.ScheduleNewWorkflow(ctx, "parentWf")
	require.NoError(t, err)

	metadata, err := client0.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	require.True(t, api.WorkflowMetadataIsComplete(metadata))

	require.True(t, c.childHistoryReceived.Load(), "child should have received propagated history")

	// All events should be in a single chunk, parallel activities don't split chunks
	require.Equal(t, int32(1), c.childChunkCount.Load(), "parallel activities should produce 1 chunk (same app)")
	childChunks, _ := c.childChunks.Load().([]api.WorkflowResult)
	childAppIDs, _ := c.childAppIDs.Load().([]string)
	actNames, _ := c.childActNames.Load().(map[string]bool)
	completedNames, _ := c.childCompletedNames.Load().(map[string]bool)

	// Verify single chunk is App0's
	require.Len(t, childChunks, 1)
	assert.True(t, childChunks[0].Found, "workflow result should be found")
	assert.Equal(t, app0AppID, childChunks[0].AppID, "chunk should be App0")
	require.Len(t, childAppIDs, 1)
	assert.Equal(t, app0AppID, childAppIDs[0])

	// All 3 parallel activities should be present as scheduled & completed
	assert.True(t, actNames["parallelAct1"])
	assert.True(t, actNames["parallelAct2"])
	assert.True(t, actNames["parallelAct3"])
	assert.True(t, completedNames["parallelAct1"])
	assert.True(t, completedNames["parallelAct2"])
	assert.True(t, completedNames["parallelAct3"])
}
