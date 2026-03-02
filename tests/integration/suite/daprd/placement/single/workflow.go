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
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(workflow))
}

type workflow struct {
	daprd *daprd.Daprd
	place *placement.Placement
}

func (w *workflow) Setup(t *testing.T) []framework.Option {
	w.place = placement.New(t)
	scheduler := scheduler.New(t)

	w.daprd = daprd.New(t,
		daprd.WithPlacementAddresses(w.place.Address()),
		daprd.WithInMemoryActorStateStore("foo"),
		daprd.WithScheduler(scheduler),
	)

	return []framework.Option{
		framework.WithProcesses(w.place, scheduler, w.daprd),
	}
}

func (w *workflow) Run(t *testing.T, ctx context.Context) {
	w.daprd.WaitUntilRunning(t, ctx)

	expTable := &placement.TableState{
		Tables: map[string]*placement.Table{
			"default": {
				Version: 1,
				Hosts: []placement.Host{
					{
						Name:      w.daprd.InternalGRPCAddress(),
						ID:        w.daprd.AppID(),
						APIVLevel: 20,
						Namespace: "default",
					},
				},
			},
		},
	}

	assert.Equal(t, expTable, w.place.PlacementTables(t, ctx))

	client := dworkflow.NewClient(w.daprd.GRPCConn(t, ctx))
	cctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	require.NoError(t, client.StartWorker(cctx, dworkflow.NewRegistry()))

	expTable.Tables["default"].Version = 2
	expTable.Tables["default"].Hosts[0].Entities = []string{
		"dapr.internal.default." + w.daprd.AppID() + ".activity",
		"dapr.internal.default." + w.daprd.AppID() + ".retentioner",
		"dapr.internal.default." + w.daprd.AppID() + ".workflow",
	}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, expTable, w.place.PlacementTables(t, ctx))
	}, time.Second*10, time.Millisecond*10)

	cancel()
	expTable.Tables["default"].Version = 3
	expTable.Tables["default"].Hosts[0].Entities = nil
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, expTable, w.place.PlacementTables(t, ctx))
	}, time.Second*10, time.Second)
}
