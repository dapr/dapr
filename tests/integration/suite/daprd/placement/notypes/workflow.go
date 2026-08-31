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
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(workflow))
}

// workflow tests the lifecycle of a daprd that starts with no actor types,
// gains types through a workflow client connection, and loses them when the
// client disconnects.
type workflow struct {
	actors *actors.Actors
}

func (w *workflow) Setup(t *testing.T) []framework.Option {
	// No actor types registered.
	w.actors = actors.New(t)

	return []framework.Option{
		framework.WithProcesses(w.actors),
	}
}

func (w *workflow) Run(t *testing.T, ctx context.Context) {
	w.actors.WaitUntilRunning(t, ctx)

	// With no entities, the placement table should have no hosts.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := w.actors.Placement().PlacementTables(t, ctx)
		if !assert.Contains(c, table.Tables, "default") {
			return
		}
		assert.Nil(c, table.Tables["default"].Hosts)
	}, time.Second*10, time.Millisecond*10)

	// Record the version after initial connection.
	table := w.actors.Placement().PlacementTables(t, ctx)
	initVersion := table.Tables["default"].Version

	// Start a workflow client which will register workflow actor types.
	client := dworkflow.NewClient(w.actors.GRPCConn(t, ctx))
	cctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	require.NoError(t, client.StartWorker(cctx, dworkflow.NewRegistry()))

	// Workflow entities should now be registered, triggering dissemination.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table = w.actors.Placement().PlacementTables(t, ctx)
		if !assert.Contains(c, table.Tables, "default") {
			return
		}
		assert.Greater(c, table.Tables["default"].Version, initVersion)
		assert.Equal(c, []placement.Host{
			{
				Name:      w.actors.Daprd().InternalGRPCAddress(),
				ID:        w.actors.Daprd().AppID(),
				APIVLevel: 20,
				Namespace: "default",
				Entities: []string{
					"dapr.internal.default." + w.actors.Daprd().AppID() + ".activity",
					"dapr.internal.default." + w.actors.Daprd().AppID() + ".retentioner",
					"dapr.internal.default." + w.actors.Daprd().AppID() + ".workflow",
				},
			},
		}, table.Tables["default"].Hosts)
	}, time.Second*10, time.Millisecond*10)

	workflowVersion := table.Tables["default"].Version

	// Disconnect the workflow client. The workflow entities should be removed
	// and the host should be removed from the table since it has no remaining
	// entities.
	cancel()
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := w.actors.Placement().PlacementTables(t, ctx)
		if !assert.Contains(c, table.Tables, "default") {
			return
		}
		assert.Greater(c, table.Tables["default"].Version, workflowVersion)
		assert.Nil(c, table.Tables["default"].Hosts)
	}, time.Second*10, time.Millisecond*10)
}
