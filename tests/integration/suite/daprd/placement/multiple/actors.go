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
	suite.Register(new(actors))
}

type actors struct {
	actors []*dactors.Actors
}

func (a *actors) Setup(t *testing.T) []framework.Option {
	actor1 := dactors.New(t,
		dactors.WithActorTypes("abc", "def"),
	)
	actor2 := dactors.New(t,
		dactors.WithActorTypes("123", "456"),
		dactors.WithPeerActor(actor1),
	)
	actor3 := dactors.New(t,
		dactors.WithActorTypes("xyz"),
		dactors.WithPeerActor(actor1),
	)

	a.actors = []*dactors.Actors{actor1, actor2, actor3}

	return []framework.Option{
		framework.WithProcesses(actor1, actor2, actor3),
	}
}

func (a *actors) Run(t *testing.T, ctx context.Context) {
	a.actors[0].WaitUntilRunning(t, ctx)
	a.actors[1].WaitUntilRunning(t, ctx)
	a.actors[2].WaitUntilRunning(t, ctx)

	expHosts := []placement.Host{
		{
			Entities:  []string{"abc", "def"},
			Name:      a.actors[0].Daprd().InternalGRPCAddress(),
			ID:        a.actors[0].Daprd().AppID(),
			APIVLevel: 20,
			Namespace: "default",
		},
		{
			Entities:  []string{"123", "456"},
			Name:      a.actors[1].Daprd().InternalGRPCAddress(),
			ID:        a.actors[1].Daprd().AppID(),
			APIVLevel: 20,
			Namespace: "default",
		},
		{
			Entities:  []string{"xyz"},
			Name:      a.actors[2].Daprd().InternalGRPCAddress(),
			ID:        a.actors[2].Daprd().AppID(),
			APIVLevel: 20,
			Namespace: "default",
		},
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, expHosts, a.actors[0].Placement().PlacementTables(t, ctx).Tables["default"].Hosts)
		assert.Equal(c, uint64(3), a.actors[0].Placement().PlacementTables(t, ctx).Tables["default"].Version)
	}, time.Second*10, time.Millisecond*10)

	client := dworkflow.NewClient(a.actors[0].Daprd().GRPCConn(t, ctx))
	cctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	require.NoError(t, client.StartWorker(cctx, dworkflow.NewRegistry()))

	expHosts[0].Entities = []string{
		"abc",
		"dapr.internal.default." + a.actors[0].Daprd().AppID() + ".activity",
		"dapr.internal.default." + a.actors[0].Daprd().AppID() + ".retentioner",
		"dapr.internal.default." + a.actors[0].Daprd().AppID() + ".workflow",
		"def",
	}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		tables := a.actors[0].Placement().PlacementTables(t, ctx)
		if !assert.Contains(c, tables.Tables, "default") {
			return
		}
		assert.Equal(c, expHosts, tables.Tables["default"].Hosts)
		assert.Equal(c, uint64(4), tables.Tables["default"].Version)
	}, time.Second*20, time.Millisecond*10)

	cancel()
	expHosts[0].Entities = []string{
		"abc",
		"def",
	}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		tables := a.actors[0].Placement().PlacementTables(t, ctx)
		if !assert.Contains(c, tables.Tables, "default") {
			return
		}
		assert.Equal(c, expHosts, tables.Tables["default"].Hosts)
		assert.Equal(c, uint64(5), tables.Tables["default"].Version)
	}, time.Second*10, time.Second)
}
