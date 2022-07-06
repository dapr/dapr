package placement

import (
	"fmt"
	"testing"
	"time"

	hcraft "github.com/hashicorp/raft"
	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/placement/raft"
	daprtesting "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/logger"
)

func TestPlacementHA(t *testing.T) {
	logger.ApplyOptionsToLoggers(&logger.Options{
		OutputLevel: "debug",
	})

	// Get 3 ports
	ports, err := daprtesting.GetFreePorts(3)
	if err != nil {
		log.Fatalf("failed to get 3 ports: %v", err)
		return
	}

	// Note that ports below are unused (i.e. no service is started on those ports), they are just used as identifiers with the IP address
	testMembers := []*raft.DaprHostMember{
		{
			Name:     "127.0.0.1:3031",
			AppID:    "testmember1",
			Entities: []string{"red"},
		},
		{
			Name:     "127.0.0.1:3032",
			AppID:    "testmember2",
			Entities: []string{"blue"},
		},
		{
			Name:     "127.0.0.1:3033",
			AppID:    "testmember3",
			Entities: []string{"red", "blue"},
		},
	}

	// Create 3 raft servers
	raftServers := make([]*raft.Server, 3)
	peers := make([]raft.PeerInfo, 3)
	for i := 0; i < 3; i++ {
		peers[i] = raft.PeerInfo{
			ID:      fmt.Sprintf("mynode-%d", i),
			Address: fmt.Sprintf("127.0.0.1:%d", ports[i]),
		}
	}
	for i := 0; i < 3; i++ {
		raftServers[i] = createRaftServer(t, i, peers)
	}

	// Run tests
	leader := -1
	t.Run("elects leader with 3 nodes", func(t *testing.T) {
		leader = findLeader(t, raftServers)
	})
	if leader == -1 {
		// If there's no leader, no point in running the other tests
		t.FailNow()
	}
	t.Run("set and retrieve state in leader", func(t *testing.T) {
		_, err := raftServers[leader].ApplyCommand(raft.MemberUpsert, *testMembers[0])
		assert.NoError(t, err)

		retrieveValidState(t, raftServers[leader], testMembers[0])
	})
	t.Run("retrieve state in follower", func(t *testing.T) {
		time.Sleep(2500 * time.Millisecond)
		follower := (leader + 1) % 3
		retrieveValidState(t, raftServers[follower], testMembers[0])
	})
	t.Run("new leader after leader fails", func(t *testing.T) {
		raftServers[leader].Shutdown()
		raftServers[leader] = nil
		time.Sleep(2500 * time.Millisecond)
		leader = findLeader(t, raftServers)
	})
	t.Run("set and retrieve state in leader after re-election", func(t *testing.T) {
		_, err := raftServers[leader].ApplyCommand(raft.MemberUpsert, *testMembers[1])
		assert.NoError(t, err)

		retrieveValidState(t, raftServers[leader], testMembers[1])
	})
	t.Run("leave only leader node running", func(t *testing.T) {
		for i, srv := range raftServers {
			if srv != nil && !srv.IsLeader() {
				srv.Shutdown()
				raftServers[i] = nil
			}
		}

		// There should be no leader
		time.Sleep(2500 * time.Millisecond)
		for _, srv := range raftServers {
			if srv != nil {
				assert.False(t, srv.IsLeader())
			}
		}
	})
	t.Run("leader elected when second node comes up", func(t *testing.T) {
		i := 0
		for {
			if raftServers[i] == nil {
				break
			}
			i++
		}
		raftServers[i] = createRaftServer(t, i, peers)
		time.Sleep(2500 * time.Millisecond)
		leader = findLeader(t, raftServers)
	})
	t.Run("state is preserved", func(t *testing.T) {
		for _, srv := range raftServers {
			if srv != nil {
				retrieveValidState(t, srv, testMembers[1])
			}
		}
	})
	t.Run("leave only follower node running", func(t *testing.T) {
		for i, srv := range raftServers {
			if srv != nil && srv.IsLeader() {
				srv.Shutdown()
				raftServers[i] = nil
			}
		}

		// There should be no leader
		time.Sleep(2500 * time.Millisecond)
		for _, srv := range raftServers {
			if srv != nil {
				assert.False(t, srv.IsLeader())
			}
		}
	})
	t.Run("shutdown and restart all nodes", func(t *testing.T) {
		// Shutdown all nodes
		for _, srv := range raftServers {
			if srv != nil {
				srv.Shutdown()
			}
		}

		// Restart all nodes
		time.Sleep(2500 * time.Millisecond)
		for i := 0; i < 3; i++ {
			raftServers[i] = createRaftServer(t, i, peers)
		}
	})
	t.Run("leader is elected", func(t *testing.T) {
		leader = findLeader(t, raftServers)
	})

	// Shutdown all servers
	for _, srv := range raftServers {
		if srv != nil {
			srv.Shutdown()
		}
	}
}

func createRaftServer(t *testing.T, nodeID int, peers []raft.PeerInfo) *raft.Server {
	srv := raft.New(fmt.Sprintf("mynode-%d", nodeID), true, peers, "")
	err := srv.StartRaft(&hcraft.Config{
		ProtocolVersion:    hcraft.ProtocolVersionMax,
		HeartbeatTimeout:   250 * time.Millisecond,
		ElectionTimeout:    250 * time.Millisecond,
		CommitTimeout:      50 * time.Millisecond,
		MaxAppendEntries:   64,
		ShutdownOnRemove:   true,
		TrailingLogs:       10240,
		SnapshotInterval:   120 * time.Second,
		SnapshotThreshold:  8192,
		LeaderLeaseTimeout: 250 * time.Millisecond,
	})
	assert.NoError(t, err)
	return srv
}

func findLeader(t *testing.T, raftServers []*raft.Server) int {
	// Maximum 5 seconds
	const maxAttempts = 50

	attempts := 0
	for {
		// Ensure that one node became leader
		for i, srv := range raftServers {
			if srv != nil && srv.IsLeader() {
				t.Logf("leader elected in %d ms: server %d", attempts*100, i)
				return i
			}
		}
		attempts++
		if attempts == maxAttempts {
			t.Fatalf("did not elect a leader in %d ms", maxAttempts*100)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func retrieveValidState(t *testing.T, srv *raft.Server, expect *raft.DaprHostMember) {
	state := srv.FSM().State()
	assert.NotNil(t, state)
	actual, found := state.Members()[expect.Name]
	if !assert.True(t, found) {
		t.FailNow()
	}
	assert.Equal(t, expect.Name, actual.Name)
	assert.Equal(t, expect.AppID, actual.AppID)
	assert.EqualValues(t, expect.Entities, actual.Entities)
}
