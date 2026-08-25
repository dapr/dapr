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

package handoff

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	procscheduler "github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(deadhost))
}

// deadhost keeps a raw control plane instead of the workflow harness: the
// victim must start first and be killed mid-run, and every other daprd must
// join after, which the harness's framework-time daprd cannot express.
type deadhost struct {
	place     *placement.Placement
	scheduler *procscheduler.Scheduler
	db        *sqlite.SQLite
	victim    *daprd.Daprd
	joiners   [2]*daprd.Daprd
}

func (a *deadhost) Setup(t *testing.T) []framework.Option {
	a.place = placement.New(t)
	a.scheduler = procscheduler.New(t)
	a.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithMetadata("busyTimeout", "10s"),
		sqlite.WithMetadata("disableWAL", "true"),
	)

	appID := uuid.New().String()
	newDaprd := func() *daprd.Daprd {
		return daprd.New(t,
			daprd.WithAppID(appID),
			daprd.WithResourceFiles(a.db.GetComponent(t)),
			daprd.WithPlacementAddresses(a.place.Address()),
			daprd.WithSchedulerAddresses(a.scheduler.Address()),
			daprd.WithFeatureEnabled(t, "WorkflowsFastPath"),
			daprd.WithWorkflowJanitorPeriod(t, time.Second),
		)
	}
	// Victim and joiners start mid-run (the victim is killed, the joiners
	// trigger the rebalance): deliberately not in WithProcesses.
	a.victim = newDaprd()
	for i := range a.joiners {
		a.joiners[i] = newDaprd()
	}

	return []framework.Option{
		framework.WithProcesses(a.place, a.scheduler, a.db),
	}
}

func (a *deadhost) Run(t *testing.T, ctx context.Context) {
	a.scheduler.WaitUntilRunning(t, ctx)
	a.place.WaitUntilRunning(t, ctx)

	const instances = 4

	var executions atomic.Int64
	started := make(chan struct{}, instances)

	newWorkflow := func(r *task.TaskRegistry) {
		require.NoError(t, r.AddWorkflowN("HandoffDeadHost", func(c *task.WorkflowContext) (any, error) {
			var out string
			if err := c.CallActivity("Slow", task.WithActivityInput("Dapr")).Await(&out); err != nil {
				return nil, err
			}
			return out, nil
		}))
	}

	// The victim's bodies block until their host dies with them.
	victimRegistry := task.NewTaskRegistry()
	newWorkflow(victimRegistry)
	require.NoError(t, victimRegistry.AddActivityN("Slow", func(c task.ActivityContext) (any, error) {
		executions.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-c.Context().Done()
		return nil, c.Context().Err()
	}))

	// Survivor listeners complete the reclaimed re-execution promptly.
	newSurvivorRegistry := func() *task.TaskRegistry {
		r := task.NewTaskRegistry()
		newWorkflow(r)
		require.NoError(t, r.AddActivityN("Slow", func(task.ActivityContext) (any, error) {
			executions.Add(1)
			return "recovered", nil
		}))
		return r
	}

	a.victim.Run(t, ctx)
	t.Cleanup(func() { a.victim.Cleanup(t) })
	a.victim.WaitUntilRunning(t, ctx)

	victimClient := client.NewTaskHubGrpcClient(a.victim.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, victimClient.StartWorkItemListener(ctx, victimRegistry))

	ids := make([]string, instances)
	for i := range ids {
		resp, err := a.victim.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
			WorkflowComponent: "dapr",
			WorkflowName:      "HandoffDeadHost",
		})
		require.NoError(t, err)
		ids[i] = resp.GetInstanceId()
	}

	for range instances {
		select {
		case <-started:
		case <-time.After(time.Second * 30):
			require.Fail(t, "timed out waiting for every activity body to start executing")
		}
	}
	require.Equal(t, int64(instances), executions.Load())

	startVersion := a.place.PlacementTables(t, ctx).Tables["default"].Version

	for i, joiner := range a.joiners {
		joiner.Run(t, ctx)
		t.Cleanup(func() { joiner.Cleanup(t) })
		joiner.WaitUntilRunning(t, ctx)

		joinerClient := client.NewTaskHubGrpcClient(joiner.GRPCConn(t, ctx), backend.DefaultLogger())
		require.NoError(t, joinerClient.StartWorkItemListener(ctx, newSurvivorRegistry()))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			table := a.place.PlacementTables(t, ctx).Tables["default"]
			if !assert.NotNil(c, table) {
				return
			}
			//nolint:gosec
			assert.GreaterOrEqual(c, table.Version, startVersion+uint64(i+1))
		}, time.Second*15, time.Millisecond*10)
	}

	// Let guards spawn and heartbeat for the moved actors, then kill the
	// victim without any shutdown handling: records freeze mid-heartbeat.
	time.Sleep(time.Millisecond * 1500)
	a.victim.Kill(t)

	survivor := a.joiners[len(a.joiners)-1]
	survivorClient := client.NewTaskHubGrpcClient(survivor.GRPCConn(t, ctx), backend.DefaultLogger())
	for _, id := range ids {
		metadata, err := survivorClient.WaitForWorkflowCompletion(ctx, api.InstanceID(id))
		require.NoError(t, err,
			"the new owner must reclaim a dead host's stale record within the grace, not strand behind it")
		assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", metadata.GetRuntimeStatus().String())
		assert.Equal(t, `"recovered"`, metadata.GetOutput().GetValue())
	}

	assert.GreaterOrEqual(t, executions.Load(), int64(2*instances),
		"every body killed with its host must have been re-executed by a survivor")
}
