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
	suite.Register(new(continueasnew))
}

// continueasnew verifies that a workflow which participated in propagation
// will pass its history forward across a CAN boundary, so the new generation
// sees the prior generation's events via ctx.GetPropagatedHistory().
type continueasnew struct {
	workflow *procworkflow.Workflow

	gen2SawHistory     atomic.Bool
	gen2EventCount     atomic.Int32
	gen2HasGen1Task    atomic.Bool
	gen2HasGen1Started atomic.Bool
}

func (c *continueasnew) Setup(t *testing.T) []framework.Option {
	c.workflow = procworkflow.New(t,
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *continueasnew) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	reg := c.workflow.Registry()

	reg.AddActivityN("act", func(ctx task.ActivityContext) (any, error) {
		return "act-done", nil
	})

	reg.AddWorkflowN("can-parent", func(ctx *task.WorkflowContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}

		if input == "first" {
			if err := ctx.CallActivity("act",
				task.WithHistoryPropagation(api.PropagateLineage()),
			).Await(nil); err != nil {
				return nil, err
			}
			ctx.ContinueAsNew("second")
			return nil, nil
		}

		// Second gen: should see gen-1's events via IncomingHistory
		ph := ctx.GetPropagatedHistory()
		if ph == nil {
			return "no-history", nil
		}

		c.gen2SawHistory.Store(true)
		c.gen2EventCount.Store(int32(len(ph.Events())))
		for _, e := range ph.Events() {
			if ts := e.GetTaskScheduled(); ts != nil && ts.GetName() == "act" {
				c.gen2HasGen1Task.Store(true)
			}
			if e.GetExecutionStarted() != nil {
				c.gen2HasGen1Started.Store(true)
			}
		}
		return "done", nil
	})

	client := c.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "can-parent",
		api.WithInstanceID("can-prop"),
		api.WithInput("first"),
	)
	require.NoError(t, err)

	metadata, err := client.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	require.True(t, api.WorkflowMetadataIsComplete(metadata))

	require.True(t, c.gen2SawHistory.Load(), "second generation should receive propagated history from prior generation")
	assert.True(t, c.gen2HasGen1Task.Load(), "second generation should see the 'act' TaskScheduled event from generation 1")
	assert.True(t, c.gen2HasGen1Started.Load(), "second generation should see generation 1's ExecutionStarted")
	assert.Greater(t, c.gen2EventCount.Load(), int32(2), "propagated history should contain multiple events from generation 1")
}
