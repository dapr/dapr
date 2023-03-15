package placement

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	hcraft "github.com/hashicorp/raft"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/dapr/dapr/pkg/placement/raft"
	daprtesting "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/logger"
)

func init() {
	logger.ApplyOptionsToLoggers(&logger.Options{
		OutputLevel: "debug",
	})
}

func TestPlacementHA(t *testing.T) {
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
	ready := make([]<-chan struct{}, 3)
	raftServerCancel := make([]context.CancelFunc, 3)
	peers := make([]raft.PeerInfo, 3)
	for i := 0; i < 3; i++ {
		peers[i] = raft.PeerInfo{
			ID:      fmt.Sprintf("mynode-%d", i),
			Address: fmt.Sprintf("127.0.0.1:%d", ports[i]),
		}
	}
	for i := 0; i < 3; i++ {
		raftServers[i], ready[i], raftServerCancel[i] = createRaftServer(t, i, peers)
	}
	for i := 0; i < 3; i++ {
		select {
		case <-ready[i]:
			// nop
		case <-time.After(time.Second):
			t.Fatalf("raft server %d did not start in time", i)
		}
	}

	// Run tests
	leader := -1
	t.Run("elects leader with 3 nodes", func(t *testing.T) {
		leader = findLeader(t, raftServers)
	})

	if leader == -1 {
		// If there's no leader, no point in running the other tests
		t.Fatal("does not have a leader")
	}

	t.Run("set and retrieve state in leader", func(t *testing.T) {
		_, err := raftServers[leader].ApplyCommand(raft.MemberUpsert, *testMembers[0])
		assert.NoError(t, err)

		retrieveValidState(t, raftServers[leader], testMembers[0])
	})

	t.Run("retrieve state in follower", func(t *testing.T) {
		follower := (leader + 1) % 3
		retrieveValidState(t, raftServers[follower], testMembers[0])
	})

	t.Run("new leader after leader fails", func(t *testing.T) {
		raftServerCancel[leader]()
		raftServers[leader] = nil
		oldLeader := leader
		require.Eventually(t, func() bool {
			leader = findLeader(t, raftServers)
			return oldLeader != leader
		}, time.Second*5, time.Millisecond)
	})

	t.Run("set and retrieve state in leader after re-election", func(t *testing.T) {
		_, err := raftServers[leader].ApplyCommand(raft.MemberUpsert, *testMembers[1])
		assert.NoError(t, err)

		retrieveValidState(t, raftServers[leader], testMembers[1])
	})

	t.Run("leave only leader node running", func(t *testing.T) {
		for i, srv := range raftServers {
			if srv != nil && !srv.IsLeader() {
				raftServerCancel[i]()
				raftServers[i] = nil
				break
			}
		}

		// There should be no leader
		assert.Eventually(t, func() bool {
			for _, srv := range raftServers {
				if srv != nil && srv.IsLeader() {
					return false
				}
			}
			return true
		}, time.Second, time.Millisecond, "leader did not step down")
	})

	t.Run("leader elected when second node comes up", func(t *testing.T) {
		i := 0
		for {
			if raftServers[i] == nil {
				break
			}
			i++
		}

		raftServers[i], ready[i], raftServerCancel[i] = createRaftServer(t, i, peers)
		select {
		case <-ready[i]:
			// nop
		case <-time.After(time.Second):
			t.Fatalf("raft server %d did not start in time", i)
		}

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
				raftServerCancel[i]()
				raftServers[i] = nil
				break
			}
		}

		// There should be no leader
		for _, srv := range raftServers {
			if srv != nil {
				assert.False(t, srv.IsLeader())
			}
		}
	})

	t.Run("shutdown and restart all nodes", func(t *testing.T) {
		// Shutdown all nodes
		for i, srv := range raftServers {
			if srv != nil {
				raftServerCancel[i]()
			}
		}

		// Restart all nodes
		for i := 0; i < 3; i++ {
			raftServers[i], ready[i], raftServerCancel[i] = createRaftServer(t, i, peers)
		}
		for i := 0; i < 3; i++ {
			select {
			case <-ready[i]:
				// nop
			case <-time.After(time.Second):
				t.Fatalf("raft server %d did not start in time", i)
			}
		}
	})

	t.Run("leader is elected", func(t *testing.T) {
		leader = findLeader(t, raftServers)
	})

	// Shutdown all servers
	for i, srv := range raftServers {
		if srv != nil {
			raftServerCancel[i]()
		}
	}
}

func createRaftServer(t *testing.T, nodeID int, peers []raft.PeerInfo) (*raft.Server, <-chan struct{}, context.CancelFunc) {
	t.Helper()
	clock := clocktesting.NewFakeClock(time.Now())

	srv := raft.New(raft.Options{
		ID:           fmt.Sprintf("mynode-%d", nodeID),
		InMem:        true,
		Peers:        peers,
		LogStorePath: "",
		Clock:        clock,
	})

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		require.NoError(t, srv.StartRaft(ctx, &hcraft.Config{
			ProtocolVersion:    hcraft.ProtocolVersionMax,
			HeartbeatTimeout:   500 * time.Millisecond,
			ElectionTimeout:    500 * time.Millisecond,
			CommitTimeout:      50 * time.Millisecond,
			MaxAppendEntries:   64,
			ShutdownOnRemove:   true,
			TrailingLogs:       10240,
			SnapshotInterval:   120 * time.Second,
			SnapshotThreshold:  8192,
			LeaderLeaseTimeout: 250 * time.Millisecond,
		}))
	}()

	ready := make(chan struct{})
	go func() {
		defer wg.Done()
		defer close(ready)
		for {
			timeoutCtx, timeoutCancel := context.WithTimeout(ctx, time.Second)
			defer timeoutCancel()
			r, err := srv.Raft(timeoutCtx)
			require.NoError(t, err)
			if r.State() == hcraft.Follower || r.State() == hcraft.Leader {
				return
			}
		}
	}()

	go func() {
		defer wg.Done()
		for {
			select {
			case <-ready:
			case <-time.After(time.Millisecond):
				clock.Step(time.Second * 2)
			}
		}
	}()

	go func() {
		defer wg.Done()
		for {
			select {
			case <-time.After(time.Millisecond):
				clock.Step(time.Millisecond * 500)
			case <-ctx.Done():
				return
			}
		}
	}()

	stopped := make(chan struct{})
	go func() {
		wg.Wait()
		close(stopped)
	}()

	return srv, ready, func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(time.Second * 5):
			require.Fail(t, "server didn't stop in time")
		}
	}
}

func findLeader(t *testing.T, raftServers []*raft.Server) int {
	t.Helper()

	var n int

	// Ensure that one node became leader
	require.Eventually(t, func() bool {
		for i, srv := range raftServers {
			if srv != nil && srv.IsLeader() {
				t.Logf("leader elected server %d", i)
				n = i
				return true
			}
		}
		return false
	}, time.Second*5, time.Millisecond, "no leader elected")
	return n
}

func retrieveValidState(t *testing.T, srv *raft.Server, expect *raft.DaprHostMember) {
	t.Helper()
	var actual *raft.DaprHostMember
	assert.Eventuallyf(t, func() bool {
		state := srv.FSM().State()
		assert.NotNil(t, state)
		var found bool
		actual, found = state.Members()[expect.Name]
		return found && expect.Name == actual.Name &&
			expect.AppID == actual.AppID
	}, time.Second*5, time.Millisecond, "%v != %v", expect, actual)
	require.NotNil(t, actual)
	assert.EqualValues(t, expect.Entities, actual.Entities)
}
