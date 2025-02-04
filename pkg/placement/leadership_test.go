/*
Copyright 2024 The Dapr Authors
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

package placement

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	hashicorpRaft "github.com/hashicorp/raft"

	"github.com/dapr/dapr/pkg/placement/tests"
)

func TestCleanupHeartBeats(t *testing.T) {
	raftOpts, err := tests.RaftOpts(t)
	require.NoError(t, err)

	_, testServer, clock, cleanup := newTestPlacementServer(t, *raftOpts)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, testServer.raftNode.IsLeader())
		assert.True(c, testServer.hasLeadership.Load())
	}, time.Second*15, 10*time.Millisecond, "leader was not elected in time")

	maxClients := 3

	for i := range maxClients {
		testServer.lastHeartBeat.Store(fmt.Sprintf("ns-10.0.0.%d:1001", i), clock.Now().UnixNano())
	}

	getCount := func() int {
		cnt := 0
		testServer.lastHeartBeat.Range(func(k, v any) bool {
			cnt++
			return true
		})

		return cnt
	}

	require.Equal(t, maxClients, getCount())
	testServer.cleanupHeartbeats()
	require.Equal(t, 0, getCount())
	cleanup()
}

func TestMonitorLeadership(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	raftClusterOpts, err := tests.RaftClusterOpts(t)

	require.NoError(t, err)

	numServers := len(raftClusterOpts)
	placementServers := make([]*Service, numServers)
	cleanupFns := make([]context.CancelFunc, numServers)
	underlyingRaftServers := make([]*hashicorpRaft.Raft, numServers)

	// Setup Raft and placement servers
	for i := range numServers {
		_, placementServers[i], _, cleanupFns[i] = newTestPlacementServer(t, *raftClusterOpts[i])

		raft, err := placementServers[i].raftNode.Raft(ctx)
		require.NoError(t, err)

		underlyingRaftServers[i] = raft
	}

	// firstServerID is the initial leader
	i := 0
	var firstServerID int
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		idx := i % numServers
		i++
		assert.True(c, placementServers[idx].raftNode.IsLeader())
		assert.True(c, placementServers[idx].hasLeadership.Load())
		firstServerID = idx
	}, time.Second*15, 10*time.Millisecond, "leader was not elected in time")

	secondServerID := (firstServerID + 1) % numServers
	thirdServerID := (secondServerID + 1) % numServers

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.False(c, placementServers[secondServerID].raftNode.IsLeader())
		assert.False(c, placementServers[secondServerID].hasLeadership.Load())

		assert.False(c, placementServers[thirdServerID].raftNode.IsLeader())
		assert.False(c, placementServers[thirdServerID].hasLeadership.Load())
	}, time.Second*15, 10*time.Millisecond, "leader was not elected in time")

	// Transfer leadership away from the original leader to the second server
	underlyingRaftServers[firstServerID].LeadershipTransferToServer(hashicorpRaft.ServerID(placementServers[secondServerID].raftNode.GetID()), hashicorpRaft.ServerAddress(placementServers[secondServerID].raftNode.GetRaftBind()))

	// Verify that the placement server will properly follow the raft leadership
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, placementServers[secondServerID].raftNode.IsLeader())
		assert.True(c, placementServers[secondServerID].hasLeadership.Load())

		assert.False(c, placementServers[firstServerID].raftNode.IsLeader())
		assert.False(c, placementServers[firstServerID].hasLeadership.Load())

		assert.False(c, placementServers[thirdServerID].raftNode.IsLeader())
		assert.False(c, placementServers[thirdServerID].hasLeadership.Load())
	}, time.Second*10, 10*time.Millisecond)

	// Transfer leadership back to the original leader
	var future hashicorpRaft.Future
	require.Eventually(t, func() bool {
		future = underlyingRaftServers[secondServerID].LeadershipTransferToServer(hashicorpRaft.ServerID(placementServers[firstServerID].raftNode.GetID()), hashicorpRaft.ServerAddress(placementServers[firstServerID].raftNode.GetRaftBind()))
		return future.Error() == nil
	}, time.Second*10, 10*time.Millisecond, "raft leadership transfer timeout")

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, placementServers[firstServerID].raftNode.IsLeader())
		assert.True(c, placementServers[firstServerID].hasLeadership.Load())

		assert.False(c, placementServers[secondServerID].raftNode.IsLeader())
		assert.False(c, placementServers[secondServerID].hasLeadership.Load())

		assert.False(c, placementServers[thirdServerID].raftNode.IsLeader())
		assert.False(c, placementServers[thirdServerID].hasLeadership.Load())
	}, time.Second*10, 10*time.Millisecond, "raft server was not properly re-elected in time")

	t.Cleanup(func() {
		for i := range numServers {
			cleanupFns[i]()
		}
		cancel()
	})
}
