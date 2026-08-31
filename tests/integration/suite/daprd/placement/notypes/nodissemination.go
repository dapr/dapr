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

package notypes

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(nodissemination))
}

// nodissemination tests that multiple daprd instances with no actor types
// have no hosts in the placement table and that disconnecting does not trigger
// additional dissemination.
type nodissemination struct {
	actors1 *actors.Actors
	actors2 *actors.Actors
	actors3 *actors.Actors
}

func (n *nodissemination) Setup(t *testing.T) []framework.Option {
	n.actors1 = actors.New(t)
	n.actors2 = actors.New(t, actors.WithPeerActor(n.actors1))
	n.actors3 = actors.New(t, actors.WithPeerActor(n.actors1))

	return []framework.Option{
		framework.WithProcesses(n.actors1, n.actors2, n.actors3),
	}
}

func (n *nodissemination) Run(t *testing.T, ctx context.Context) {
	n.actors1.WaitUntilRunning(t, ctx)
	n.actors2.WaitUntilRunning(t, ctx)
	n.actors3.WaitUntilRunning(t, ctx)

	// No daprd has actor types, so the placement table should have no hosts.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := n.actors1.Placement().PlacementTables(t, ctx)
		if !assert.Contains(c, table.Tables, "default") {
			return
		}
		assert.Nil(c, table.Tables["default"].Hosts)
	}, time.Second*10, time.Millisecond*10)

	// Record the current version.
	table := n.actors1.Placement().PlacementTables(t, ctx)
	versionBefore := table.Tables["default"].Version

	// Kill daprd2 and daprd3. Since they had no entities, their disconnection
	// should not trigger dissemination and version should remain unchanged.
	n.actors2.Daprd().Cleanup(t)
	n.actors3.Daprd().Cleanup(t)

	// Give some time for any erroneous dissemination to propagate.
	time.Sleep(time.Millisecond * 500)

	table = n.actors1.Placement().PlacementTables(t, ctx)
	assert.Contains(t, table.Tables, "default")
	assert.Nil(t, table.Tables["default"].Hosts)
	assert.Equal(t, versionBefore, table.Tables["default"].Version)
}
