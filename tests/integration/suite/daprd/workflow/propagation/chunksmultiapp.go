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
	suite.Register(new(chunksmultiapp))
}

// chunksmultiapp tests that PropagatedHistory chunks correctly track event
// provenance across a 3-app chain: App0 -> App1 -> App2. Each app runs an
// activity before forwarding to the next app via PropagateLineage.
// App2 (the leaf) should see 2 chunks:
//   - Chunk 0: App0's events (appID=App0, events[0..5])
//   - Chunk 1: App1's events (appID=App1, events[6..11])
type chunksmultiapp struct {
	workflow *procworkflow.Workflow

	leafHistoryReceived atomic.Bool
	leafTotalEvents     atomic.Int32
	leafChunkCount      atomic.Int32

	leafChunks     atomic.Value
	leafAppIDs     atomic.Value
	eventCountApp0 atomic.Int32
	eventCountApp1 atomic.Int32
	leafApp0HasAct atomic.Bool
	leafApp1HasAct atomic.Bool
}

func (c *chunksmultiapp) Setup(t *testing.T) []framework.Option {
	c.workflow = procworkflow.New(t,
		procworkflow.WithDaprds(3),
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *chunksmultiapp) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	app0Reg := c.workflow.Registry()
	app1Reg := c.workflow.RegistryN(1)
	app2Reg := c.workflow.RegistryN(2)

	app0AppID := c.workflow.DaprN(0).AppID()
	app1AppID := c.workflow.DaprN(1).AppID()

	// App0: root workflow calls app0Act, then creates middleWf on App1
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

	// App1: middle workflow calls app1Act, then creates leafWf on App2
	// w/ PropagateLineage so App0's chunks are forwarded to App2
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
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&result); err != nil {
			return nil, err
		}
		return result, nil
	})

	// App2: leaf wf reads full propagated chain & verifies chunks
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

		// Verify app0's events contain app0Act
		for _, e := range app0Events {
			if ts := e.GetTaskScheduled(); ts != nil && ts.GetName() == "app0Act" {
				c.leafApp0HasAct.Store(true)
			}
		}
		// Verify app1's events contain app1Act
		for _, e := range app1Events {
			if ts := e.GetTaskScheduled(); ts != nil && ts.GetName() == "app1Act" {
				c.leafApp1HasAct.Store(true)
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

	// 12 total events: 6 from App0 + 6 from App1 = 2 chunks
	assert.Equal(t, int32(12), c.leafTotalEvents.Load(), "App2 should receive 12 events: 6 from App0 + 6 from App1")
	assert.Equal(t, int32(2), c.leafChunkCount.Load(), "should have 2 chunks (App0 + App1)")
	leafChunks, _ := c.leafChunks.Load().([]*api.WorkflowResult)
	leafAppIDs, _ := c.leafAppIDs.Load().([]string)

	// Verify chunks and app events
	require.Len(t, leafChunks, 2, "expected exactly 2 chunks")
	assert.True(t, leafChunks[0].Found, "chunk 0 should be found")
	assert.Equal(t, app0AppID, leafChunks[0].AppID, "chunk 0 should be App0")
	assert.True(t, leafChunks[1].Found, "chunk 1 should be found")
	assert.Equal(t, app1AppID, leafChunks[1].AppID, "chunk 1 should be App1")
	require.Len(t, leafAppIDs, 2, "expected 2 unique app IDs")
	assert.Equal(t, app0AppID, leafAppIDs[0], "first app should be App0 (root)")
	assert.Equal(t, app1AppID, leafAppIDs[1], "second app should be App1 (middle)")
	assert.Equal(t, int32(6), c.eventCountApp0.Load(), "EventsByAppID(App0) should return 6 events")
	assert.Equal(t, int32(6), c.eventCountApp1.Load(), "EventsByAppID(App1) should return 6 events")
	assert.True(t, c.leafApp0HasAct.Load(), "App0's events should contain app0Act")
	assert.True(t, c.leafApp1HasAct.Load(), "App1's events should contain app1Act")
}
