package placement

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	hcraft "github.com/hashicorp/raft"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/dapr/dapr/pkg/placement/raft"
	"github.com/dapr/dapr/pkg/security/fake"
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
	t.Run("elects leader with 3 nodes", func(t *testing.T) {
		// It is painful that we have to include a `time.Sleep` here, but due to
		// the non-deterministic behaviour of the raft library we are using we will
		// later fail on slower test runner machines. A clock timer wait means we
		// have a _better_ chance of being in the right spot in the state machine
		// and the network has died down. Ideally we should move to a different
		// raft library that is more deterministic and reliable for our use case.
		time.Sleep(time.Second * 3)
		require.NotEqual(t, -1, findLeader(t, raftServers))
	})

	t.Run("set and retrieve state in leader", func(t *testing.T) {
		assert.Eventually(t, func() bool {
			lead := findLeader(t, raftServers)
			_, err := raftServers[lead].ApplyCommand(raft.MemberUpsert, *testMembers[0])
			if errors.Is(err, hcraft.ErrLeadershipLost) || errors.Is(err, hcraft.ErrNotLeader) {
				// If leadership is lost, we should retry
				return false
			}
			require.NoError(t, err)

			retrieveValidState(t, raftServers[lead], testMembers[0])
			return true
		}, time.Second*10, time.Millisecond*300)
	})

	t.Run("retrieve state in follower", func(t *testing.T) {
		var follower, oldLeader int
		oldLeader = findLeader(t, raftServers)
		follower = (oldLeader + 1) % 3
		retrieveValidState(t, raftServers[follower], testMembers[0])

		t.Run("new leader after leader fails", func(t *testing.T) {
			raftServerCancel[oldLeader]()
			raftServers[oldLeader] = nil

			// It is painful that we have to include a `time.Sleep` here, but due to
			// the non-deterministic behaviour of the raft library we are using we will
			// later fail on slower test runner machines. A clock timer wait means we
			// have a _better_ chance of being in the right spot in the state machine
			// and the network has died down. Ideally we should move to a different
			// raft library that is more deterministic and reliable for our use case.
			time.Sleep(time.Second * 3)

			require.Eventually(t, func() bool {
				return oldLeader != findLeader(t, raftServers)
			}, time.Second*10, time.Millisecond*100)
		})
	})

	t.Run("set and retrieve state in leader after re-election", func(t *testing.T) {
		assert.Eventually(t, func() bool {
			_, err := raftServers[findLeader(t, raftServers)].ApplyCommand(raft.MemberUpsert, *testMembers[1])
			if errors.Is(err, hcraft.ErrLeadershipLost) || errors.Is(err, hcraft.ErrNotLeader) {
				// If leadership is lost, we should retry
				return false
			}
			require.NoError(t, err)

			retrieveValidState(t, raftServers[findLeader(t, raftServers)], testMembers[1])
			return true
		}, time.Second*10, time.Millisecond*300)
	})

	t.Run("leave only leader node running", func(t *testing.T) {
		// It is painful that we have to include a `time.Sleep` here, but due to
		// the non-deterministic behaviour of the raft library we are using we will
		// fail in a few lines on slower test runner machines. A clock timer wait
		// means we have a _better_ chance of being in the right spot in the state
		// machine. Ideally we should move to a different raft library that is more
		// deterministic and reliable.
		time.Sleep(time.Second * 3)
		leader := findLeader(t, raftServers)
		for i := range raftServers {
			if i != leader {
				raftServerCancel[i]()
				raftServers[i] = nil
			}
		}

		var running int
		for i := 0; i < 3; i++ {
			if raftServers[i] != nil {
				running++
			}
		}
		assert.Equal(t, 1, running, "only single server should be running")

		// There should be no leader
		assert.Eventually(t, func() bool {
			for _, srv := range raftServers {
				if srv != nil && srv.IsLeader() {
					return false
				}
			}
			return true
		}, time.Second*5, time.Millisecond*100, "leader did not step down")
	})

	// It is painful that we have to include a `time.Sleep` here, but due to
	// the non-deterministic behaviour of the raft library we are using we will
	// fail in a few lines on slower test runner machines. A clock timer wait
	// means we have a _better_ chance of being in the right spot in the state
	// machine. Ideally we should move to a different raft library that is more
	// deterministic and reliable.
	time.Sleep(time.Second * 3)

	t.Run("leader elected when second node comes up", func(t *testing.T) {
		var oldSvr int
		for oldSvr = 0; oldSvr < 3; oldSvr++ {
			if raftServers[oldSvr] == nil {
				break
			}
		}

		raftServers[oldSvr], ready[oldSvr], raftServerCancel[oldSvr] = createRaftServer(t, oldSvr, peers)
		select {
		case <-ready[oldSvr]:
			// nop
		case <-time.After(time.Second * 5):
			t.Fatalf("raft server %d did not start in time", oldSvr)
		}

		var running int
		for i := 0; i < 3; i++ {
			if raftServers[i] != nil {
				running++
			}
		}
		assert.Equal(t, 2, running, "only two servers should be running")

		findLeader(t, raftServers)
	})

	t.Run("state is preserved", func(t *testing.T) {
		for _, srv := range raftServers {
			if srv != nil {
				retrieveValidState(t, srv, testMembers[1])
			}
		}
	})

	t.Run("leave only follower node running", func(t *testing.T) {
		leader := findLeader(t, raftServers)
		for i, srv := range raftServers {
			if i != leader && srv != nil {
				raftServerCancel[i]()
				raftServers[i] = nil
			}
		}

		assert.Eventually(t, func() bool {
			// There should be no leader
			for _, srv := range raftServers {
				if srv != nil {
					return !srv.IsLeader()
				}
			}
			return false
		}, time.Second*5, time.Millisecond*100, "leader did not step down")
	})

	t.Run("shutdown and restart all nodes", func(t *testing.T) {
		// Shutdown all nodes
		for i, srv := range raftServers {
			if srv != nil {
				raftServerCancel[i]()
			}
		}

		// It is painful that we have to include a `time.Sleep` here, but due to
		// the non-deterministic behaviour of the raft library we are using we will
		// later fail on slower test runner machines. A clock timer wait means we
		// have a _better_ chance of being in the right spot in the state machine
		// and the network has died down. Ideally we should move to a different
		// raft library that is more deterministic and reliable for our use case.
		time.Sleep(time.Second * 3)

		// Restart all nodes
		for i := 0; i < 3; i++ {
			raftServers[i], ready[i], raftServerCancel[i] = createRaftServer(t, i, peers)
		}

		for i := 0; i < 3; i++ {
			select {
			case <-ready[i]:
				// nop
			case <-time.After(time.Second * 5):
				t.Fatalf("raft server %d did not start in time", i)
			}
		}
	})

	t.Run("leader is elected", func(t *testing.T) {
		findLeader(t, raftServers)
	})

	// Shutdown all servers
	for i, srv := range raftServers {
		if srv != nil {
			raftServerCancel[i]()
		}
	}
}

func createRaftServer(t *testing.T, nodeID int, peers []raft.PeerInfo) (*raft.Server, <-chan struct{}, context.CancelFunc) {
	clock := clocktesting.NewFakeClock(time.Now())

	srv := raft.New(raft.Options{
		ID:           fmt.Sprintf("mynode-%d", nodeID),
		InMem:        true,
		Peers:        peers,
		LogStorePath: "",
		Clock:        clock,
	})

	ctx, cancel := context.WithCancel(context.Background())

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		require.NoError(t, srv.StartRaft(ctx, fake.New(), &hcraft.Config{
			ProtocolVersion:    hcraft.ProtocolVersionMax,
			HeartbeatTimeout:   2 * time.Second,
			ElectionTimeout:    2 * time.Second,
			CommitTimeout:      2 * time.Second,
			MaxAppendEntries:   64,
			ShutdownOnRemove:   true,
			TrailingLogs:       10240,
			SnapshotInterval:   120 * time.Second,
			SnapshotThreshold:  8192,
			LeaderLeaseTimeout: time.Second,
			BatchApplyCh:       true,
		}))
	}()

	ready := make(chan struct{})
	go func() {
		defer close(ready)
		for {
			timeoutCtx, timeoutCancel := context.WithTimeout(ctx, time.Second*5)
			defer timeoutCancel()
			r, err := srv.Raft(timeoutCtx)
			require.NoError(t, err)
			if r.State() == hcraft.Follower || r.State() == hcraft.Leader {
				return
			}
		}
	}()

	go func() {
		for {
			select {
			case <-ready:
			case <-time.After(time.Millisecond):
				clock.Step(time.Second * 2)
			}
		}
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
	// Ensure that one node became leader
	n := -1
	require.Eventually(t, func() bool {
		for i, srv := range raftServers {
			if srv != nil && srv.IsLeader() {
				n = i
				break
			}
		}

		if n == -1 {
			return false
		}

		// Ensure there is only a single leader.
		for i, srv := range raftServers {
			if i != n && srv != nil && srv.IsLeader() {
				require.Fail(t, "more than one leader")
			}
		}

		return true
	}, time.Second*30, time.Second, "no leader elected")
	return n
}

func retrieveValidState(t *testing.T, srv *raft.Server, expect *raft.DaprHostMember) {
	var actual *raft.DaprHostMember
	assert.Eventuallyf(t, func() bool {
		state := srv.FSM().State()
		assert.NotNil(t, state)
		var found bool
		actual, found = state.Members()[expect.Name]
		return found && expect.Name == actual.Name &&
			expect.AppID == actual.AppID
	}, time.Second*5, time.Millisecond*300, "%v != %v", expect, actual)
	require.NotNil(t, actual)
	assert.EqualValues(t, expect.Entities, actual.Entities)
}
