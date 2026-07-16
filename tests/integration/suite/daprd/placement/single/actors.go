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
	suite.Register(new(actors))
}

type actors struct {
	actors *dactors.Actors
}

func (a *actors) Setup(t *testing.T) []framework.Option {
	a.actors = dactors.New(t,
		dactors.WithActorTypes("abc", "def"),
	)

	return []framework.Option{
		framework.WithProcesses(a.actors),
	}
}

func (a *actors) Run(t *testing.T, ctx context.Context) {
	a.actors.WaitUntilRunning(t, ctx)

	expTable := &placement.TableState{
		Tables: map[string]*placement.Table{
			"default": {
				Version: 1,
				Hosts: []placement.Host{
					{
						Entities:  []string{"abc", "def"},
						Name:      a.actors.Daprd().InternalGRPCAddress(),
						ID:        a.actors.Daprd().AppID(),
						APIVLevel: 20,
						Namespace: "default",
					},
				},
			},
		},
	}

	assert.Equal(t, expTable, a.actors.Placement().PlacementTables(t, ctx))

	client := dworkflow.NewClient(a.actors.Daprd().GRPCConn(t, ctx))
	cctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	require.NoError(t, client.StartWorker(cctx, dworkflow.NewRegistry()))

	expTable.Tables["default"].Version = 2
	expTable.Tables["default"].Hosts[0].Entities = []string{
		"abc",
		"dapr.internal.default." + a.actors.Daprd().AppID() + ".activity",
		"dapr.internal.default." + a.actors.Daprd().AppID() + ".retentioner",
		"dapr.internal.default." + a.actors.Daprd().AppID() + ".workflow",
		"def",
	}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, expTable, a.actors.Placement().PlacementTables(t, ctx))
	}, time.Second*10, time.Millisecond*10)

	cancel()
	expTable.Tables["default"].Version = 3
	expTable.Tables["default"].Hosts[0].Entities = []string{
		"abc",
		"def",
	}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, expTable, a.actors.Placement().PlacementTables(t, ctx))
	}, time.Second*10, time.Second)
}
