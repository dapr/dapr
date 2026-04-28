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
	suite.Register(new(chunks))
}

// chunks tests that PropagatedHistory chunks correctly track which app
// produced which events when history crosses app boundaries. App0 -> App1 w/ PropagateLineage
// Chunks reflect the parent chain, not the receiver. App1's childWf is the
// receiver, it hasn't produced events yet, and it does not add itself to
// the history it received. So App1 sees exactly one chunk: App0's, covering
// all of App0's events.
type chunks struct {
	workflow *procworkflow.Workflow

	childHistoryReceived atomic.Bool
	childTotalEvents     atomic.Int32
	childChunkCount      atomic.Int32

	app0Chunks atomic.Value
	app0AppIDs atomic.Value
}

func (c *chunks) Setup(t *testing.T) []framework.Option {
	c.workflow = procworkflow.New(t,
		procworkflow.WithDaprds(2),
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *chunks) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	app0Reg := c.workflow.Registry()
	app1Reg := c.workflow.RegistryN(1)

	app0AppID := c.workflow.DaprN(0).AppID()

	app0Reg.AddActivityN("app0Act", func(ctx task.ActivityContext) (any, error) {
		return "done", nil
	})

	app0Reg.AddWorkflowN("parentWf", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("app0Act").Await(nil); err != nil {
			return nil, err
		}
		var result string
		if err := ctx.CallChildWorkflow("childWf",
			task.WithChildWorkflowAppID(c.workflow.DaprN(1).AppID()),
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&result); err != nil {
			return nil, err
		}
		return result, nil
	})

	app1Reg.AddWorkflowN("childWf", func(ctx *task.WorkflowContext) (any, error) {
		ph := ctx.GetPropagatedHistory()
		if ph == nil {
			return "no history", nil
		}

		c.childHistoryReceived.Store(true)
		c.childTotalEvents.Store(int32(len(ph.Events())))
		workflows := ph.GetWorkflows()
		c.childChunkCount.Store(int32(len(workflows)))

		c.app0Chunks.Store(workflows)
		c.app0AppIDs.Store(ph.GetAppIDs())

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

	// App0's history: 6 events
	require.Equal(t, int32(6), c.childTotalEvents.Load(), "child should receive 6 events from App0")
	// Should have exactly 1 chunk — App0's events
	require.Equal(t, int32(1), c.childChunkCount.Load(), "should have 1 chunk (App0 only)")
	app0Chunks, _ := c.app0Chunks.Load().([]*api.WorkflowResult)
	app0AppIDs, _ := c.app0AppIDs.Load().([]string)
	require.Len(t, app0Chunks, 1)
	assert.True(t, app0Chunks[0].Found, "workflow result should be found")
	assert.Equal(t, app0AppID, app0Chunks[0].AppID, "chunk should be tagged with App0's appID")
	require.Len(t, app0AppIDs, 1)
	assert.Equal(t, app0AppID, app0AppIDs[0], "AppIDs should contain App0")
}
