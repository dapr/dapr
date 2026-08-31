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

package single

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
	actors *dactors.Actors
}

func (w *workflow) Setup(t *testing.T) []framework.Option {
	w.actors = dactors.New(t,
		dactors.WithActorTypes("mytype"),
	)

	return []framework.Option{
		framework.WithProcesses(w.actors),
	}
}

func (w *workflow) Run(t *testing.T, ctx context.Context) {
	w.actors.WaitUntilRunning(t, ctx)

	expTable := &placement.TableState{
		Tables: map[string]*placement.Table{
			"default": {
				Version: 1,
				Hosts: []placement.Host{
					{
						Entities:  []string{"mytype"},
						Name:      w.actors.Daprd().InternalGRPCAddress(),
						ID:        w.actors.Daprd().AppID(),
						APIVLevel: 20,
						Namespace: "default",
					},
				},
			},
		},
	}

	assert.Equal(t, expTable, w.actors.Placement().PlacementTables(t, ctx))

	client := dworkflow.NewClient(w.actors.Daprd().GRPCConn(t, ctx))
	cctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	require.NoError(t, client.StartWorker(cctx, dworkflow.NewRegistry()))

	expTable.Tables["default"].Version = 2
	expTable.Tables["default"].Hosts[0].Entities = []string{
		"dapr.internal.default." + w.actors.Daprd().AppID() + ".activity",
		"dapr.internal.default." + w.actors.Daprd().AppID() + ".retentioner",
		"dapr.internal.default." + w.actors.Daprd().AppID() + ".workflow",
		"mytype",
	}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, expTable, w.actors.Placement().PlacementTables(t, ctx))
	}, time.Second*10, time.Millisecond*10)

	cancel()
	expTable.Tables["default"].Version = 3
	expTable.Tables["default"].Hosts[0].Entities = []string{"mytype"}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, expTable, w.actors.Placement().PlacementTables(t, ctx))
	}, time.Second*10, time.Second)
}
