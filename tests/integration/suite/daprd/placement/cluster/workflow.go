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

package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/placement/cluster"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(workflow))
}

type workflow struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	place  *cluster.Cluster
}

func (w *workflow) Setup(t *testing.T) []framework.Option {
	w.place = cluster.New(t)
	scheduler := scheduler.New(t)

	w.daprd1 = daprd.New(t,
		daprd.WithPlacementAddresses(w.place.Addresses()...),
		daprd.WithInMemoryActorStateStore("foo"),
		daprd.WithScheduler(scheduler),
	)
	w.daprd2 = daprd.New(t,
		daprd.WithPlacementAddresses(w.place.Addresses()...),
		daprd.WithInMemoryActorStateStore("foo"),
		daprd.WithScheduler(scheduler),
	)

	return []framework.Option{
		framework.WithProcesses(w.place, scheduler, w.daprd1, w.daprd2),
	}
}

func (w *workflow) Run(t *testing.T, ctx context.Context) {
	w.daprd1.WaitUntilRunning(t, ctx)
	w.daprd2.WaitUntilRunning(t, ctx)

	leader := w.place.Leader(t, ctx)

	// Neither daprd has actor types registered, so the placement table should
	// have no hosts.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := leader.PlacementTables(t, ctx)
		if !assert.Contains(c, table.Tables, "default") {
			return
		}
		assert.Nil(c, table.Tables["default"].Hosts)
	}, time.Second*10, time.Millisecond*10)

	// Record the version before starting workflow clients.
	table := leader.PlacementTables(t, ctx)
	versionBefore := table.Tables["default"].Version

	client1 := dworkflow.NewClient(w.daprd1.GRPCConn(t, ctx))
	cctx1, cancel1 := context.WithCancel(ctx)
	t.Cleanup(cancel1)
	require.NoError(t, client1.StartWorker(cctx1, dworkflow.NewRegistry()))

	//nolint:prealloc
	expHosts := []placement.Host{
		{
			Name:      w.daprd1.InternalGRPCAddress(),
			ID:        w.daprd1.AppID(),
			APIVLevel: 20,
			Namespace: "default",
			Entities: []string{
				"dapr.internal.default." + w.daprd1.AppID() + ".activity",
				"dapr.internal.default." + w.daprd1.AppID() + ".retentioner",
				"dapr.internal.default." + w.daprd1.AppID() + ".workflow",
			},
		},
	}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table = leader.PlacementTables(t, ctx)
		if !assert.Contains(c, table.Tables, "default") {
			return
		}
		assert.Greater(c, table.Tables["default"].Version, versionBefore)
		assert.Equal(c, expHosts, table.Tables["default"].Hosts)
	}, time.Second*10, time.Millisecond*10)

	versionAfterWf1 := table.Tables["default"].Version

	client2 := dworkflow.NewClient(w.daprd2.GRPCConn(t, ctx))
	cctx2, cancel2 := context.WithCancel(ctx)
	t.Cleanup(cancel2)
	require.NoError(t, client2.StartWorker(cctx2, dworkflow.NewRegistry()))

	expHosts = append(expHosts,
		placement.Host{
			Name:      w.daprd2.InternalGRPCAddress(),
			ID:        w.daprd2.AppID(),
			APIVLevel: 20,
			Namespace: "default",
			Entities: []string{
				"dapr.internal.default." + w.daprd2.AppID() + ".activity",
				"dapr.internal.default." + w.daprd2.AppID() + ".retentioner",
				"dapr.internal.default." + w.daprd2.AppID() + ".workflow",
			},
		},
	)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table = leader.PlacementTables(t, ctx)
		if !assert.Contains(c, table.Tables, "default") {
			return
		}
		assert.Greater(c, table.Tables["default"].Version, versionAfterWf1)
		assert.Equal(c, expHosts, table.Tables["default"].Hosts)
	}, time.Second*10, time.Second)

	versionAfterWf2 := table.Tables["default"].Version

	// After workflow1 stops, daprd1 has no entities and is removed from the
	// placement table.
	cancel1()
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table = leader.PlacementTables(t, ctx)
		if !assert.Contains(c, table.Tables, "default") {
			return
		}
		assert.Greater(c, table.Tables["default"].Version, versionAfterWf2)
		assert.Equal(c, []placement.Host{
			{
				Name:      w.daprd2.InternalGRPCAddress(),
				ID:        w.daprd2.AppID(),
				APIVLevel: 20,
				Namespace: "default",
				Entities: []string{
					"dapr.internal.default." + w.daprd2.AppID() + ".activity",
					"dapr.internal.default." + w.daprd2.AppID() + ".retentioner",
					"dapr.internal.default." + w.daprd2.AppID() + ".workflow",
				},
			},
		}, table.Tables["default"].Hosts)
	}, time.Second*10, time.Millisecond*10)

	versionAfterCancel1 := table.Tables["default"].Version

	// After workflow2 stops, daprd2 has no entities and is removed from the
	// placement table.
	cancel2()
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table = leader.PlacementTables(t, ctx)
		if !assert.Contains(c, table.Tables, "default") {
			return
		}
		assert.Greater(c, table.Tables["default"].Version, versionAfterCancel1)
		assert.Nil(c, table.Tables["default"].Hosts)
	}, time.Second*10, time.Millisecond*10)
}
