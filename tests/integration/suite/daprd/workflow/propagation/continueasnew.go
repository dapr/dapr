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

// CAN only forwards an incoming chunk. A root workflow that
// schedules activities with PropagateLineage but itself has no parent
// chunk does NOT seed a chunk for its next generation.
type continueasnew struct {
	workflow *procworkflow.Workflow

	gen2Ran        atomic.Bool
	gen2SawHistory atomic.Bool
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
			// Root workflow that opts into propagation for its activity.
			if err := ctx.CallActivity("act",
				task.WithHistoryPropagation(api.PropagateLineage()),
			).Await(nil); err != nil {
				return nil, err
			}
			ctx.ContinueAsNew("second")
			return nil, nil
		}

		// Second generation. Because gen-1 was a root (no IncomingHistory),
		// gen-2 must NOT receive a chunk — propagation across CAN only
		// inherits what was already there, not anything synthesized.
		c.gen2Ran.Store(true)
		if ctx.GetPropagatedHistory() != nil {
			c.gen2SawHistory.Store(true)
		}
		return statusDone, nil
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

	require.True(t, c.gen2Ran.Load(), "second generation should have executed")
	assert.False(t, c.gen2SawHistory.Load(),
		"root workflow's CAN must not synthesize a chunk for the next generation; "+
			"propagation across CAN only inherits an incoming chunk from a parent")
}
