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

package workflow

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ExecutorActorType returns the executor actor type of the daprd at index:
// registered by a daprd with a local executor under
// WorkflowsClusteredDeployment or WorkflowsStagedOperations.
func (w *Workflow) ExecutorActorType(index int) string {
	return "dapr.internal.default." + w.daprds[index].AppID() + ".executor"
}

// HostsActorType returns whether the daprd at index hosts actorType, as its
// actor runtime metadata reports. A failed metadata read fails c and returns
// false.
func (w *Workflow) HostsActorType(c assert.TestingT, ctx context.Context, index int, actorType string) bool {
	_, hosted := w.DaprN(index).ActiveActorCount(c, ctx, actorType)
	return hosted
}

// WaitForActorTypePlaced waits until the daprd at index hosts actorType, with
// placed, or no longer does, and until the placement tables the daprds
// receive show it: the placement service's table or, with the scheduler
// serving placement, a dissemination after before, a PlacementVersion read
// before the change. The tables a daprd has applied may still lag the
// authority's: a test which depends on one daprd's own view waits on that
// daprd too.
func (w *Workflow) WaitForActorTypePlaced(t *testing.T, ctx context.Context, index int, actorType string, placed bool, before uint64) {
	t.Helper()

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, placed, w.HostsActorType(c, ctx, index, actorType))
	}, time.Second*10, time.Millisecond*10)

	if w.place == nil {
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Greater(c, w.PlacementVersion(t, ctx), before)
		}, time.Second*10, time.Millisecond*10)
		return
	}

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		table := w.place.PlacementTables(t, ctx).Tables["default"]
		if !assert.NotNil(c, table) {
			return
		}
		var found bool
		for _, host := range table.Hosts {
			if slices.Contains(host.Entities, actorType) {
				found = true
			}
		}
		assert.Equal(c, placed, found)
	}, time.Second*10, time.Millisecond*10)
}
