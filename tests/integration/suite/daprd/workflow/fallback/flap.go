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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
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

// flapDamping is well above a flap cycle's connected time, a few placement
// rounds, so the damping period never runs out while the worker flaps.
const flapDamping = 5 * time.Second

func (f *flap) Setup(t *testing.T) []framework.Option {
	f.workflow = newPair(t, flapDamping, fallbackSettle)

	return []framework.Option{
		framework.WithProcesses(f.workflow),
	}
}

func (f *flap) Run(t *testing.T, ctx context.Context) {
	f.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, f.workflow.RegistryN(0).AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		return doneOutput, nil
	}))

	workflowType := f.workflow.WorkflowActorType(1)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, f.workflow.HostsActorType(c, ctx, 1, workflowType))
	}, time.Second*10, time.Millisecond*10)

	// Watch the daprd without a worker throughout the flapping.
	unhosted := f.workflow.WatchActorTypeHosted(t, ctx, 1, workflowType)

	executorType := f.workflow.ExecutorActorType(0)
	mgmt := f.workflow.ManagementClientN(t, ctx, 1)
	const rounds = 5
	ids := make([]api.InstanceID, 0, rounds)
	for range rounds {
		before := f.workflow.PlacementVersion(t, ctx)
		unlocks := f.unlocks(t, ctx)
		worker := f.workflow.ConnectWorkerN(t, ctx, 0, f.workflow.RegistryN(0))
		f.workflow.WaitForConnectedWorkersN(t, ctx, 0, 1)
		f.workflow.WaitForActorTypePlaced(t, ctx, 0, executorType, true, before)
		f.waitUnlocked(t, ctx, unlocks)

		// Calls through the fallback host keep working.
		sctx, cancel := context.WithTimeout(ctx, time.Second*10)
		id, err := mgmt.ScheduleNewWorkflow(sctx, "wf", api.WithStartTime(time.Now()))
		cancel()
		require.NoError(t, err)
		ids = append(ids, id)

		before = f.workflow.PlacementVersion(t, ctx)
		unlocks = f.unlocks(t, ctx)
		worker.Disconnect(t)
		f.workflow.WaitForNoConnectedWorkersN(t, ctx, 0)
		f.workflow.WaitForActorTypePlaced(t, ctx, 0, executorType, false, before)
		// The fallback host has received the round without the executor
		// before the next connect: a round which merged the leave with the
		// connect would leave its damping period running from one cycle
		// into the next.
		f.waitUnlocked(t, ctx, unlocks)
	}

	assert.False(t, unhosted(), "the daprd without a worker gave the workflow actor types up while the worker flapped")

	// A worker which stays: the fallback host gives the types up, but not
	// before the damping period.
	connected := time.Now()
	f.workflow.ConnectWorkerN(t, ctx, 0, f.workflow.RegistryN(0))
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.False(c, f.workflow.HostsActorType(c, ctx, 1, workflowType))
	}, flapDamping+time.Second*20, time.Millisecond*10)
	assert.GreaterOrEqual(t, time.Since(connected), flapDamping-time.Second)

	fworkflow.WaitForAllCompleted(t, ctx, mgmt, ids...)
}

// unlocks returns how many unlock orders, which end a placement round, the
// fallback host, daprd 1, has received, as its
// runtime/actor/table_operation_recv_total metric counts them. A failed
// read fails c.
func (f *flap) unlocks(c assert.TestingT, ctx context.Context) float64 {
	return f.workflow.DaprN(1).Metrics(c, ctx).SumWithLabels(
		"dapr_runtime_actor_table_operation_recv_total", "operation:unlock")
}

// waitUnlocked waits until the fallback host, daprd 1, has received an
// unlock order past from, an unlocks count read before a change of daprd 0:
// the unlock which ends the placement round of the change, or that of a
// later round, as under either placement protocol each round ends with an
// unlock to every daprd. The placement authority may show a change the
// fallback host has not received yet. Daprd counts an order as it receives
// it, before handling it, and handles its orders one at a time, so it
// handles the lock of the round of the next change only after this unlock:
// the next change reaches it in a round of its own.
func (f *flap) waitUnlocked(t *testing.T, ctx context.Context, from float64) {
	t.Helper()

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Greater(c, f.unlocks(c, ctx), from)
	}, time.Second*10, time.Millisecond*10)
}
