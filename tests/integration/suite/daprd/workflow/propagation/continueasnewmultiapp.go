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
	suite.Register(new(continueasnewmultiapp))
}

// continueasnewmultiapp covers: a child on App1 receives
// propagated history from a parent on App0, then does ContinueAsNew. The
// new generation must still see App0's chunk in its IncomingHistory plus
// the prior App1 generation's events. This exercises both:
//   - preservation of incoming history across CAN
//   - accumulation of the prior generation's events into the new chain
type continueasnewmultiapp struct {
	workflow *procworkflow.Workflow

	app0AppID      string
	gen1SawApp0    atomic.Bool
	gen2SawHistory atomic.Bool
	gen2EventCount atomic.Int64
	gen2SawApp0    atomic.Bool
	gen2SawGen1    atomic.Bool
}

func (c *continueasnewmultiapp) Setup(t *testing.T) []framework.Option {
	c.workflow = procworkflow.New(t,
		procworkflow.WithDaprds(2),
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *continueasnewmultiapp) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)
	c.app0AppID = c.workflow.DaprN(0).AppID()

	app0Reg := c.workflow.Registry()
	app1Reg := c.workflow.RegistryN(1)

	app0Reg.AddActivityN("app0Marker", func(ctx task.ActivityContext) (any, error) {
		return "marker", nil
	})

	app0Reg.AddWorkflowN("rootApp0", func(ctx *task.WorkflowContext) (any, error) {
		// Schedule an activity locally so App0's history has identifiable
		// events for App1's child to verify after CAN.
		if err := ctx.CallActivity("app0Marker").Await(nil); err != nil {
			return nil, err
		}
		var out string
		return out, ctx.CallChildWorkflow("childApp1",
			task.WithChildWorkflowAppID(c.workflow.DaprN(1).AppID()),
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&out)
	})

	app1Reg.AddWorkflowN("childApp1", func(ctx *task.WorkflowContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}

		if input == "" {
			// First generation: confirm we received App0's chunk, then CAN.
			ph := ctx.GetPropagatedHistory()
			if ph != nil {
				for _, chunk := range ph.GetWorkflows() {
					if chunk.AppID == c.app0AppID {
						c.gen1SawApp0.Store(true)
						break
					}
				}
			}
			ctx.ContinueAsNew("gen2")
			return nil, nil
		}

		// Second generation (post-CAN): must still see App0's chunk PLUS the
		// prior generation's own events.
		ph := ctx.GetPropagatedHistory()
		if ph == nil {
			return statusNoHistoryHyphen, nil
		}

		c.gen2SawHistory.Store(true)
		c.gen2EventCount.Store(int64(len(ph.Events())))
		for _, chunk := range ph.GetWorkflows() {
			if chunk.AppID == c.app0AppID {
				c.gen2SawApp0.Store(true)
			}
			if chunk.Name == "childApp1" {
				c.gen2SawGen1.Store(true)
			}
		}
		return statusDone, nil
	})

	client0 := c.workflow.BackendClient(t, ctx)
	c.workflow.BackendClientN(t, ctx, 1)

	id, err := client0.ScheduleNewWorkflow(ctx, "rootApp0",
		api.WithInstanceID("can-multi"),
	)
	require.NoError(t, err)

	metadata, err := client0.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	require.True(t, api.WorkflowMetadataIsComplete(metadata))

	require.True(t, c.gen1SawApp0.Load(), "first generation of App1 child should see App0 chunk in propagated history")
	require.True(t, c.gen2SawHistory.Load(), "second generation should see propagated history (chain preserved across CAN)")
	assert.True(t, c.gen2SawApp0.Load(), "second generation should still see App0's original chunk")
	assert.True(t, c.gen2SawGen1.Load(), "second generation should see prior App1 generation's events")
	assert.Positive(t, c.gen2EventCount.Load())
}
