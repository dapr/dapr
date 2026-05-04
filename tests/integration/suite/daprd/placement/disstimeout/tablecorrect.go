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

package disstimeout

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	dactors "github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(tablecorrect))
}

// tablecorrect tests that the placement table is correct after two
// timeout+reconnect cycles. After recovery, the daprd must appear in the
// server's placement table with the correct actor types, name, and namespace.
// The blocker must NOT appear (it was disconnected by the server timeout).
type tablecorrect struct {
	actors *dactors.Actors
	place  *placement.Placement
}

func (tc *tablecorrect) Setup(t *testing.T) []framework.Option {
	tc.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second*2),
	)

	tc.actors = dactors.New(t,
		dactors.WithActorTypes("typeA", "typeB"),
		dactors.WithPlacement(tc.place),
	)

	return []framework.Option{
		framework.WithProcesses(tc.actors),
	}
}

func (tc *tablecorrect) Run(t *testing.T, ctx context.Context) {
	tc.actors.WaitUntilRunning(t, ctx)

	expHost := placement.Host{
		Entities:  []string{"typeA", "typeB"},
		Name:      tc.actors.Daprd().InternalGRPCAddress(),
		ID:        tc.actors.Daprd().AppID(),
		APIVLevel: 20,
		Namespace: "default",
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := tc.place.PlacementTables(t, ctx)
		if !assert.NotNil(c, table.Tables["default"]) {
			return
		}
		assert.Equal(c, []placement.Host{expHost}, table.Tables["default"].Hosts)
	}, time.Second*15, time.Millisecond*100)

	connectBlocker := func(t *testing.T, id string) {
		t.Helper()
		client := tc.place.Client(t, ctx)
		blocker, err := client.ReportDaprStatus(ctx)
		require.NoError(t, err)
		require.NoError(t, blocker.Send(&v1pb.Host{
			Name: id, Port: 9999,
			Entities: []string{"typeA"}, Id: id, Namespace: "default",
		}))
		go func() {
			for {
				if _, err := blocker.Recv(); err != nil {
					return
				}
			}
		}()
	}

	for i := range 2 {
		connectBlocker(t, "blocker-tc-"+string(rune('a'+i)))
		time.Sleep(4 * time.Second)
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := tc.place.PlacementTables(t, ctx)
		if !assert.NotNil(c, table.Tables["default"]) {
			return
		}
		assert.Equal(c, []placement.Host{expHost}, table.Tables["default"].Hosts)
	}, time.Second*10, time.Millisecond*500)
}
