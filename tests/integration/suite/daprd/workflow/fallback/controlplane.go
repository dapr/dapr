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

package fallback

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(controlplane))
}

// controlplane restarts the scheduler, then the placement service, while a
// fallback host holds staged instances. The starts are staged before the
// scheduler restart and the events after it, so both the reminders the
// scheduler persisted and those it took once back are kept. While placement
// is down and once it is back, the daprd stays a fallback host, and once a
// worker connects every instance completes with the event raised for it.
type controlplane struct {
	workflow  *workflow.Workflow
	placeBack *placement.Placement
}

func (c *controlplane) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t,
		workflow.WithStagedOperations(),
		withSettle(t, 0),
		workflow.WithPlacementService(),
	)
	c.placeBack = placementBack(t, c.workflow)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *controlplane) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, c.workflow.Registry().AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		var in string
		if err := ctx.GetInput(&in); err != nil {
			return nil, err
		}
		var ev string
		if err := ctx.WaitForSingleEvent("go", time.Hour).Await(&ev); err != nil {
			return nil, err
		}
		var out string
		if err := ctx.CallActivity("act", task.WithActivityInput(in+"-"+ev)).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, c.workflow.Registry().AddActivityN("act", func(ctx task.ActivityContext) (any, error) {
		var in string
		if err := ctx.GetInput(&in); err != nil {
			return nil, err
		}
		return in, nil
	}))

	require.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.True(col, c.workflow.HostsActorType(col, ctx, 0, c.workflow.WorkflowActorType(0)))
	}, time.Second*10, time.Millisecond*50)

	mgmt := c.workflow.ManagementClient(t, ctx)

	const instances = 10
	ids := make([]api.InstanceID, 0, instances)
	for i := range instances {
		sctx, cancel := context.WithTimeout(ctx, time.Second*10)
		id, err := mgmt.ScheduleNewWorkflow(sctx, "wf",
			api.WithInstanceID(api.InstanceID(fmt.Sprintf("fallback-controlplane-%d", i))),
			api.WithInput(fmt.Sprintf("in-%d", i)),
		)
		cancel()
		require.NoError(t, err)
		ids = append(ids, id)
	}

	c.workflow.Scheduler().RestartGraceful(t, ctx)
	c.workflow.Scheduler().WaitUntilRunning(t, ctx)

	// The events are staged once the daprd reconnected to the scheduler; a
	// raise retried after a lost reply raises the same event again, and the
	// workflow takes the first.
	for i, id := range ids {
		require.EventuallyWithT(t, func(col *assert.CollectT) {
			rctx, cancel := context.WithTimeout(ctx, time.Second*3)
			defer cancel()
			assert.NoError(col, mgmt.RaiseEvent(rctx, id, "go", api.WithEventPayload(fmt.Sprintf("ev-%d", i))))
		}, time.Second*20, time.Millisecond*100, id)
	}
	waitPending(t, ctx, mgmt, time.Second*20, ids...)
	assert.True(t, c.workflow.HostsActorType(t, ctx, 0, c.workflow.WorkflowActorType(0)))

	c.workflow.Placement().Cleanup(t)
	require.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.Equal(col, "placement: disconnected", placementStatus(col, ctx, c.workflow, 0))
	}, time.Second*20, time.Millisecond*50)
	assert.True(t, c.workflow.HostsActorType(t, ctx, 0, c.workflow.WorkflowActorType(0)))

	runPlacementBack(t, ctx, c.placeBack)
	require.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.Equal(col, "placement: connected", placementStatus(col, ctx, c.workflow, 0))
		assert.True(col, c.workflow.HostsActorType(col, ctx, 0, c.workflow.WorkflowActorType(0)))
	}, time.Second*20, time.Millisecond*50)
	waitPending(t, ctx, mgmt, time.Second*20, ids...)

	c.workflow.ConnectWorker(t, ctx, c.workflow.Registry())

	for i, id := range ids {
		wctx, cancel := context.WithTimeout(ctx, time.Second*20)
		meta := fworkflow.WaitForCompleted(t, wctx, mgmt, id)
		cancel()
		assert.Equal(t, fmt.Sprintf(`"in-%d-ev-%d"`, i, i), meta.GetOutput().GetValue(), id)
	}
}
