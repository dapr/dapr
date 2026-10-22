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
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(flap))
}

// flap connects and disconnects a worker on one daprd of an app over and
// over, each time for less than the damping period, as a crash looping
// worker does. The app's other daprd, which has no worker, stays a fallback
// host throughout rather than giving the workflow actor types up and taking
// them back on each connect and leave. Once the worker stays connected for
// the damping period, it gives them up.
type flap struct {
	workflow *workflow.Workflow
}

// flapDamping is well above a flap cycle's connected time, about a second
// and a half, so the damping period never runs out while the worker flaps.
const flapDamping = 10 * time.Second

func (f *flap) Setup(t *testing.T) []framework.Option {
	f.workflow = newPair(t, flapDamping, fallbackSettle)

	return []framework.Option{
		framework.WithProcesses(f.workflow),
	}
}

func (f *flap) Run(t *testing.T, ctx context.Context) {
	f.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, f.workflow.RegistryN(0).AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		return "done", nil
	}))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, hostsWorkflows(c, ctx, f.workflow, 1))
	}, time.Second*10, time.Millisecond*50)

	// Watch the daprd without a worker throughout the flapping.
	var unhosted atomic.Bool
	wctx, wcancel := context.WithCancel(ctx)
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		for wctx.Err() == nil {
			var rerr readErrors
			if !hostsWorkflows(&rerr, wctx, f.workflow, 1) && !rerr.failed {
				unhosted.Store(true)
			}
			time.Sleep(time.Millisecond * 50)
		}
	}()

	mgmt := f.workflow.ManagementClientN(t, ctx, 1)
	var ids []api.InstanceID
	for range 5 {
		before := f.workflow.PlacementVersion(t, ctx)
		worker := f.workflow.ConnectWorkerN(t, ctx, 0, f.workflow.RegistryN(0))
		f.workflow.WaitForConnectedWorkersN(t, ctx, 0, 1)
		f.waitExecutorPlaced(t, ctx, before, true)

		// Calls through the fallback host keep working.
		sctx, cancel := context.WithTimeout(ctx, time.Second*10)
		id, err := mgmt.ScheduleNewWorkflow(sctx, "wf", api.WithStartTime(time.Now()))
		cancel()
		require.NoError(t, err)
		ids = append(ids, id)

		time.Sleep(time.Second)
		before = f.workflow.PlacementVersion(t, ctx)
		worker.Disconnect(t)
		f.workflow.WaitForNoConnectedWorkersN(t, ctx, 0)
		// The leave reaches the placement tables before the next connect:
		// a placement round which merged the two would leave the fallback
		// host's damping period running from one cycle into the next.
		f.waitExecutorPlaced(t, ctx, before, false)
		time.Sleep(time.Millisecond * 500)
	}

	wcancel()
	<-watched
	assert.False(t, unhosted.Load(), "the daprd without a worker gave the workflow actor types up while the worker flapped")

	// A worker which stays: the fallback host gives the types up, but not
	// before the damping period.
	connected := time.Now()
	f.workflow.ConnectWorkerN(t, ctx, 0, f.workflow.RegistryN(0))
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.False(c, hostsWorkflows(c, ctx, f.workflow, 1))
	}, flapDamping+time.Second*20, time.Millisecond*50)
	assert.GreaterOrEqual(t, time.Since(connected), flapDamping-time.Second)

	assertCompleted(t, ctx, mgmt, ids...)
}

// waitExecutorPlaced waits until daprd 0, the one the worker connects to,
// has registered the executor actor type, with placed, or no longer has,
// and until the placement tables the app's daprds receive show it: the
// placement service's table, or, with the scheduler serving placement, a
// dissemination after before, a PlacementVersion read before the change.
// The executor actor type is how the fallback host learns that another
// daprd has an executor.
func (f *flap) waitExecutorPlaced(t *testing.T, ctx context.Context, before uint64, placed bool) {
	t.Helper()

	executorType := "dapr.internal.default." + f.workflow.DaprN(0).AppID() + ".executor"

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, hosted := f.workflow.DaprN(0).ActiveActorCount(c, ctx, executorType)
		assert.Equal(c, placed, hosted)
	}, time.Second*10, time.Millisecond*10)

	if !f.workflow.HasPlacement() {
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Greater(c, f.workflow.PlacementVersion(t, ctx), before)
		}, time.Second*10, time.Millisecond*10)
		return
	}

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		table := f.workflow.Placement().PlacementTables(t, ctx).Tables["default"]
		if !assert.NotNil(c, table) {
			return
		}
		var found bool
		for _, host := range table.Hosts {
			if slices.Contains(host.Entities, executorType) {
				found = true
			}
		}
		assert.Equal(c, placed, found)
	}, time.Second*10, time.Millisecond*10)
}
