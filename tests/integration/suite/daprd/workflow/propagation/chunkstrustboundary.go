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
	suite.Register(new(chunkstrustboundary))
}

// chunkstrustboundary tests that chunk provenance respects trust boundaries.
// Chain: App0 -> App1 (PropagateLineage) -> App2 (PropagateOwnHistory).
// App1 chooses not to forward App0's chunk, so App2 sees only App1's chunk.
type chunkstrustboundary struct {
	workflow *procworkflow.Workflow

	leafHistoryReceived atomic.Bool
	leafTotalEvents     atomic.Int32
	leafChunkCount      atomic.Int32

	leafChunks     atomic.Value
	leafAppIDs     atomic.Value
	eventCountApp0 atomic.Int32
	eventCountApp1 atomic.Int32
	leafHasApp0Act atomic.Bool
	leafHasApp1Act atomic.Bool
}

func (c *chunkstrustboundary) Setup(t *testing.T) []framework.Option {
	c.workflow = procworkflow.New(t,
		procworkflow.WithDaprds(3),
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *chunkstrustboundary) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	app0Reg := c.workflow.Registry()
	app1Reg := c.workflow.RegistryN(1)
	app2Reg := c.workflow.RegistryN(2)

	app0AppID := c.workflow.DaprN(0).AppID()
	app1AppID := c.workflow.DaprN(1).AppID()

	// App0: root, calls app0Act, then creates middleWf on App1 with lineage
	app0Reg.AddActivityN("app0Act", func(ctx task.ActivityContext) (any, error) {
		return "app0-done", nil
	})
	app0Reg.AddWorkflowN("rootWf", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("app0Act").Await(nil); err != nil {
			return nil, err
		}
		var result string
		if err := ctx.CallChildWorkflow("middleWf",
			task.WithChildWorkflowAppID(c.workflow.DaprN(1).AppID()),
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&result); err != nil {
			return nil, err
		}
		return result, nil
	})

	// App1: middle, calls app1Act, then creates leafWf on App2
	// Uses PropagateOwnHistory (trust boundary): App0's history is NOT forwarded
	app1Reg.AddActivityN("app1Act", func(ctx task.ActivityContext) (any, error) {
		return "app1-done", nil
	})
	app1Reg.AddWorkflowN("middleWf", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("app1Act").Await(nil); err != nil {
			return nil, err
		}
		var result string
		if err := ctx.CallChildWorkflow("leafWf",
			task.WithChildWorkflowAppID(c.workflow.DaprN(2).AppID()),
			task.WithHistoryPropagation(api.PropagateOwnHistory()), // trust boundary
		).Await(&result); err != nil {
			return nil, err
		}
		return result, nil
	})

	// App2: leaf, should only see App1's chunk, not App0's
	app2Reg.AddWorkflowN("leafWf", func(ctx *task.WorkflowContext) (any, error) {
		ph := ctx.GetPropagatedHistory()
		if ph == nil {
			return "no history", nil
		}

		c.leafHistoryReceived.Store(true)
		c.leafTotalEvents.Store(int32(len(ph.Events())))
		workflows := ph.GetWorkflows()
		c.leafChunkCount.Store(int32(len(workflows)))

		c.leafChunks.Store(workflows)
		c.leafAppIDs.Store(ph.GetAppIDs())

		app0Events := ph.GetEventsByAppID(app0AppID)
		app1Events := ph.GetEventsByAppID(app1AppID)
		c.eventCountApp0.Store(int32(len(app0Events)))
		c.eventCountApp1.Store(int32(len(app1Events)))

		for _, e := range app0Events {
			if ts := e.GetTaskScheduled(); ts != nil && ts.GetName() == "app0Act" {
				c.leafHasApp0Act.Store(true)
			}
		}
		for _, e := range app1Events {
			if ts := e.GetTaskScheduled(); ts != nil && ts.GetName() == "app1Act" {
				c.leafHasApp1Act.Store(true)
			}
		}

		return "done", nil
	})

	client0 := c.workflow.BackendClient(t, ctx)
	c.workflow.BackendClientN(t, ctx, 1)
	c.workflow.BackendClientN(t, ctx, 2)

	id, err := client0.ScheduleNewWorkflow(ctx, "rootWf")
	require.NoError(t, err)

	metadata, err := client0.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	assert.True(t, c.leafHistoryReceived.Load(), "App2 should have received propagated history")

	// Only App1's events (6 events), NOT App0's
	assert.Equal(t, int32(6), c.leafTotalEvents.Load(), "App2 should receive 6 events from App1 only")

	// Only 1 chunk, App1's events (App0's chunk excluded by trust boundary)
	assert.Equal(t, int32(1), c.leafChunkCount.Load(), "should have 1 chunk (App1 only, trust boundary excludes App0)")
	leafChunks, _ := c.leafChunks.Load().([]*api.WorkflowResult)
	leafAppIDs, _ := c.leafAppIDs.Load().([]string)

	// single chunk is App1's
	require.Len(t, leafChunks, 1, "expected exactly 1 chunk")
	assert.True(t, leafChunks[0].Found, "workflow result should be found")
	assert.Equal(t, app1AppID, leafChunks[0].AppID, "chunk should be App1 (not App0)")
	require.Len(t, leafAppIDs, 1, "expected 1 app ID")
	assert.Equal(t, app1AppID, leafAppIDs[0], "only App1 should be in the chain")

	assert.Equal(t, int32(0), c.eventCountApp0.Load(), "EventsByAppID(App0) should return 0 events (trust boundary)")
	assert.Equal(t, int32(6), c.eventCountApp1.Load(), "EventsByAppID(App1) should return 6 events")

	// App0's activity should NOT be visible
	assert.False(t, c.leafHasApp0Act.Load(), "App0's app0Act should NOT be visible (trust boundary)")
	// App1's activity should be visible
	assert.True(t, c.leafHasApp1Act.Load(), "App1's app1Act should be visible")
}
