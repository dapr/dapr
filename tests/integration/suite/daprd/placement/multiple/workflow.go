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

package multiple

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	dactors "github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(workflow))
}

type workflow struct {
	actors1 *dactors.Actors
	actors2 *dactors.Actors
}

func (w *workflow) Setup(t *testing.T) []framework.Option {
	w.actors1 = dactors.New(t,
		dactors.WithActorTypes("mytype"),
	)
	w.actors2 = dactors.New(t,
		dactors.WithActorTypes("mytype"),
		dactors.WithPeerActor(w.actors1),
	)

	return []framework.Option{
		framework.WithProcesses(w.actors1, w.actors2),
	}
}

func (w *workflow) Run(t *testing.T, ctx context.Context) {
	w.actors1.WaitUntilRunning(t, ctx)
	w.actors2.WaitUntilRunning(t, ctx)

	expTable := &placement.TableState{
		Tables: map[string]*placement.Table{
			"default": {
				Version: 2,
				Hosts: []placement.Host{
					{
						Entities:  []string{"mytype"},
						Name:      w.actors1.Daprd().InternalGRPCAddress(),
						ID:        w.actors1.Daprd().AppID(),
						APIVLevel: 20,
						Namespace: "default",
					},
					{
						Entities:  []string{"mytype"},
						Name:      w.actors2.Daprd().InternalGRPCAddress(),
						ID:        w.actors2.Daprd().AppID(),
						APIVLevel: 20,
						Namespace: "default",
					},
				},
			},
		},
	}

	assert.Equal(t, expTable, w.actors1.Placement().PlacementTables(t, ctx))

	client1 := dworkflow.NewClient(w.actors1.Daprd().GRPCConn(t, ctx))
	cctx1, cancel1 := context.WithCancel(ctx)
	t.Cleanup(cancel1)
	require.NoError(t, client1.StartWorker(cctx1, dworkflow.NewRegistry()))
	expTable.Tables["default"].Version = 3
	expTable.Tables["default"].Hosts[0].Entities = []string{
		"dapr.internal.default." + w.actors1.Daprd().AppID() + ".activity",
		"dapr.internal.default." + w.actors1.Daprd().AppID() + ".retentioner",
		"dapr.internal.default." + w.actors1.Daprd().AppID() + ".workflow",
		"mytype",
	}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, expTable, w.actors1.Placement().PlacementTables(t, ctx))
	}, time.Second*10, time.Millisecond*10)

	client2 := dworkflow.NewClient(w.actors2.Daprd().GRPCConn(t, ctx))
	cctx2, cancel2 := context.WithCancel(ctx)
	t.Cleanup(cancel2)
	require.NoError(t, client2.StartWorker(cctx2, dworkflow.NewRegistry()))
	expTable.Tables["default"].Version = 4
	expTable.Tables["default"].Hosts[1].Entities = []string{
		"dapr.internal.default." + w.actors2.Daprd().AppID() + ".activity",
		"dapr.internal.default." + w.actors2.Daprd().AppID() + ".retentioner",
		"dapr.internal.default." + w.actors2.Daprd().AppID() + ".workflow",
		"mytype",
	}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, expTable, w.actors1.Placement().PlacementTables(t, ctx))
	}, time.Second*20, time.Millisecond*10)

	cancel1()
	expTable.Tables["default"].Version = 5
	expTable.Tables["default"].Hosts[0].Entities = []string{"mytype"}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, expTable, w.actors1.Placement().PlacementTables(t, ctx))
	}, time.Second*10, time.Second)

	cancel2()
	expTable.Tables["default"].Version = 6
	expTable.Tables["default"].Hosts[1].Entities = []string{"mytype"}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, expTable, w.actors1.Placement().PlacementTables(t, ctx))
	}, time.Second*10, time.Second)
}
