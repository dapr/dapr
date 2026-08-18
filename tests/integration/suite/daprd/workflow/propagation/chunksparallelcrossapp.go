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
	suite.Register(new(chunksparallelcrossapp))
}

// chunksparallelcrossapp tests that parallel cross-app child workflows each
// receive independent copies of the parent's propagated history. App0 calls
// child1 on App1 and child2 on App2 in parallel, both w/ PropagateLineage.
// Each child should independently see App0's single chunk.
type chunksparallelcrossapp struct {
	workflow *procworkflow.Workflow

	// App1's child
	app1HistoryReceived atomic.Bool
	app1TotalEvents     atomic.Int64
	app1ChunkCount      atomic.Int64
	app1Chunks          atomic.Value
	app1AppIDs          atomic.Value
	app1HasAct          atomic.Bool

	// App2's child
	app2HistoryReceived atomic.Bool
	app2TotalEvents     atomic.Int64
	app2ChunkCount      atomic.Int64
	app2Chunks          atomic.Value
	app2AppIDs          atomic.Value
	app2HasAct          atomic.Bool
}

func (c *chunksparallelcrossapp) Setup(t *testing.T) []framework.Option {
	c.workflow = procworkflow.New(t,
		procworkflow.WithDaprds(3),
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *chunksparallelcrossapp) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	app0Reg := c.workflow.Registry()
	app1Reg := c.workflow.RegistryN(1)
	app2Reg := c.workflow.RegistryN(2)
	app0AppID := c.workflow.DaprN(0).AppID()

	// App0: root, calls app0Act, then fan-out to App1 and App2 in parallel
	app0Reg.AddActivityN(activityApp0Act, func(ctx task.ActivityContext) (any, error) {
		return statusDone, nil
	})
	app0Reg.AddWorkflowN("parentWf", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity(activityApp0Act).Await(nil); err != nil {
			return nil, err
		}

		// Fan-out: call child workflows on App1 & App2 in parallel
		t1 := ctx.CallChildWorkflow("child1",
			task.WithChildWorkflowAppID(c.workflow.DaprN(1).AppID()),
			task.WithHistoryPropagation(api.PropagateLineage()),
		)
		t2 := ctx.CallChildWorkflow("child2",
			task.WithChildWorkflowAppID(c.workflow.DaprN(2).AppID()),
			task.WithHistoryPropagation(api.PropagateLineage()),
		)

		// Fan-in: await both
		if err := t1.Await(nil); err != nil {
			return nil, err
		}
		if err := t2.Await(nil); err != nil {
			return nil, err
		}
		return statusDone, nil
	})

	// App1: child1 verifies it receives App0's chunk
	app1Reg.AddWorkflowN("child1", func(ctx *task.WorkflowContext) (any, error) {
		ph := ctx.GetPropagatedHistory()
		if ph == nil {
			return statusNoHistory, nil
		}

		c.app1HistoryReceived.Store(true)
		c.app1TotalEvents.Store(int64(len(ph.Events())))
		workflows := ph.GetWorkflows()
		c.app1ChunkCount.Store(int64(len(workflows)))
		for _, e := range ph.Events() {
			if ts := e.GetTaskScheduled(); ts != nil && ts.GetName() == activityApp0Act {
				c.app1HasAct.Store(true)
			}
		}

		c.app1Chunks.Store(workflows)
		c.app1AppIDs.Store(ph.GetAppIDs())

		return statusDone, nil
	})

	// App2: child2 verifies it also receives App0's chunk independently
	app2Reg.AddWorkflowN("child2", func(ctx *task.WorkflowContext) (any, error) {
		ph := ctx.GetPropagatedHistory()
		if ph == nil {
			return statusNoHistory, nil
		}

		c.app2HistoryReceived.Store(true)
		c.app2TotalEvents.Store(int64(len(ph.Events())))
		workflows2 := ph.GetWorkflows()
		c.app2ChunkCount.Store(int64(len(workflows2)))
		for _, e := range ph.Events() {
			if ts := e.GetTaskScheduled(); ts != nil && ts.GetName() == activityApp0Act {
				c.app2HasAct.Store(true)
			}
		}

		c.app2Chunks.Store(workflows2)
		c.app2AppIDs.Store(ph.GetAppIDs())

		return statusDone, nil
	})

	client0 := c.workflow.BackendClient(t, ctx)
	c.workflow.BackendClientN(t, ctx, 1)
	c.workflow.BackendClientN(t, ctx, 2)

	id, err := client0.ScheduleNewWorkflow(ctx, "parentWf")
	require.NoError(t, err)

	metadata, err := client0.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))

	// App1
	assert.True(t, c.app1HistoryReceived.Load(), "App1 should have received propagated history")
	assert.Equal(t, int64(1), c.app1ChunkCount.Load(), "App1 should have 1 chunk (App0)")
	assert.True(t, c.app1HasAct.Load(), "App1 should see app0Act")
	app1Chunks, _ := c.app1Chunks.Load().([]*api.WorkflowResult)
	app1AppIDs, _ := c.app1AppIDs.Load().([]string)
	require.Len(t, app1Chunks, 1)
	assert.True(t, app1Chunks[0].Found, "App1's workflow result should be found")
	assert.Equal(t, app0AppID, app1Chunks[0].AppID, "App1's chunk should be App0")
	require.Len(t, app1AppIDs, 1)
	assert.Equal(t, app0AppID, app1AppIDs[0])

	// App2 assertions
	assert.True(t, c.app2HistoryReceived.Load(), "App2 should have received propagated history")
	assert.Equal(t, int64(1), c.app2ChunkCount.Load(), "App2 should have 1 chunk (App0)")
	assert.True(t, c.app2HasAct.Load(), "App2 should see app0Act")
	app2Chunks, _ := c.app2Chunks.Load().([]*api.WorkflowResult)
	app2AppIDs, _ := c.app2AppIDs.Load().([]string)
	require.Len(t, app2Chunks, 1)
	assert.True(t, app2Chunks[0].Found, "App2's workflow result should be found")
	assert.Equal(t, app0AppID, app2Chunks[0].AppID, "App2's chunk should be App0")
	require.Len(t, app2AppIDs, 1)
	assert.Equal(t, app0AppID, app2AppIDs[0])

	assert.GreaterOrEqual(t, c.app1TotalEvents.Load(), int64(6),
		"App1 should receive at least 6 events from App0")
	assert.GreaterOrEqual(t, c.app2TotalEvents.Load(), int64(6),
		"App2 should receive at least 6 events from App0")
}
