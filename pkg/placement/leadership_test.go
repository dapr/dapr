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

	"github.com/stretchr/testify/require"

	hashicorpRaft "github.com/hashicorp/raft"

	"github.com/dapr/dapr/pkg/placement/tests"
)

func TestCleanupHeartBeats(t *testing.T) {
	testRaftServer := tests.Raft(t)
	_, testServer, clock, cleanup := newTestPlacementServer(t, testRaftServer)
	testServer.hasLeadership.Store(true)
	maxClients := 3

	for i := 0; i < maxClients; i++ {
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
	raftServers, err := tests.RaftCluster(t, ctx)
	require.NoError(t, err)

	numServers := len(raftServers)
	placementServers := make([]*Service, numServers)
	cleanupFns := make([]context.CancelFunc, numServers)
	leaderChannels := make([]<-chan bool, numServers)
	underlyingRaftServers := make([]*hashicorpRaft.Raft, numServers)

	// Setup Raft and placement servers
	for i := 0; i < numServers; i++ {
		raft, err := raftServers[i].Raft(ctx)
		require.NoError(t, err)
		leaderChannels[i] = raft.LeaderCh()
		underlyingRaftServers[i] = raft

		_, placementServers[i], _, cleanupFns[i] = newTestPlacementServer(t, raftServers[i])
		go placementServers[i].MonitorLeadership(ctx)
	}

	// Find the initial leader
	i := 0
	var leaderIdx int
	require.Eventually(t, func() bool {
		idx := i % numServers
		i++
		if raftServers[idx].IsLeader() && placementServers[idx].hasLeadership.Load() {
			leaderIdx = idx
			return true
		}
		return false
	}, time.Second*15, 10*time.Millisecond, "leader was not elected in time")

	secondServerID := (leaderIdx + 1) % numServers

	// We need to verify that the placement server will properly follow the raft leadership
	// Transfer leadership away from the original leader to the second server
	underlyingRaftServers[leaderIdx].LeadershipTransferToServer(hashicorpRaft.ServerID(raftServers[secondServerID].GetID()), hashicorpRaft.ServerAddress(raftServers[secondServerID].GetRaftBind()))
	require.Eventually(t, func() bool {
		return raftServers[secondServerID].IsLeader() && placementServers[secondServerID].hasLeadership.Load()
	}, time.Second*15, 10*time.Millisecond, "raft server two was not set as leader in time")

	// Transfer leadership back to the original leader
	var future hashicorpRaft.Future
	require.Eventually(t, func() bool {
		future = underlyingRaftServers[secondServerID].LeadershipTransferToServer(hashicorpRaft.ServerID(raftServers[leaderIdx].GetID()), hashicorpRaft.ServerAddress(raftServers[leaderIdx].GetRaftBind()))
		return future.Error() == nil
	}, time.Second*15, 500*time.Millisecond, "raft leadership transfer timeout")

	require.Eventually(t, func() bool {
		return raftServers[leaderIdx].IsLeader() && placementServers[leaderIdx].hasLeadership.Load()
	}, time.Second*15, 10*time.Millisecond, "server was not properly re-elected in time")

	t.Cleanup(func() {
		for i := 0; i < numServers; i++ {
			cleanupFns[i]()
		}
		cancel()
	})
}
