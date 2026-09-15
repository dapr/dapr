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

	expHosts := []placement.Host{
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
	}

	var version uint64
	expectTable := func(c *assert.CollectT) {
		table := w.actors1.PlacementTables(t, ctx).Tables["default"]
		if !assert.NotNil(c, table) {
			return
		}
		assert.ElementsMatch(c, expHosts, table.Hosts)
		assert.Greater(c, table.Version, version)
	}
	capture := func() {
		if table := w.actors1.PlacementTables(t, ctx).Tables["default"]; table != nil {
			version = table.Version
		}
	}

	require.EventuallyWithT(t, expectTable, time.Second*10, time.Millisecond*10)
	capture()

	client1 := dworkflow.NewClient(w.actors1.Daprd().GRPCConn(t, ctx))
	cctx1, cancel1 := context.WithCancel(ctx)
	t.Cleanup(cancel1)
	require.NoError(t, client1.StartWorker(cctx1, dworkflow.NewRegistry()))
	expHosts[0].Entities = []string{
		"dapr.internal.default." + w.actors1.Daprd().AppID() + ".activity",
		"dapr.internal.default." + w.actors1.Daprd().AppID() + ".retentioner",
		"dapr.internal.default." + w.actors1.Daprd().AppID() + ".workflow",
		"mytype",
	}
	assert.EventuallyWithT(t, expectTable, time.Second*10, time.Millisecond*10)
	capture()

	client2 := dworkflow.NewClient(w.actors2.Daprd().GRPCConn(t, ctx))
	cctx2, cancel2 := context.WithCancel(ctx)
	t.Cleanup(cancel2)
	require.NoError(t, client2.StartWorker(cctx2, dworkflow.NewRegistry()))
	expHosts[1].Entities = []string{
		"dapr.internal.default." + w.actors2.Daprd().AppID() + ".activity",
		"dapr.internal.default." + w.actors2.Daprd().AppID() + ".retentioner",
		"dapr.internal.default." + w.actors2.Daprd().AppID() + ".workflow",
		"mytype",
	}
	assert.EventuallyWithT(t, expectTable, time.Second*20, time.Millisecond*10)
	capture()

	cancel1()
	expHosts[0].Entities = []string{"mytype"}
	assert.EventuallyWithT(t, expectTable, time.Second*10, time.Second)
	capture()

	cancel2()
	expHosts[1].Entities = []string{"mytype"}
	assert.EventuallyWithT(t, expectTable, time.Second*10, time.Second)
}
