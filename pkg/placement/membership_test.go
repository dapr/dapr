/*
Copyright 2021 The Dapr Authors
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

//nolint:forbidigo,nosnakecase
package placement

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/dapr/dapr/pkg/placement/raft"
	"github.com/dapr/dapr/pkg/placement/tests"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

func cleanupStates(testRaftServer *raft.Server) {
	state := testRaftServer.FSM().State()
	state.Lock.RLock()
	defer state.Lock.RUnlock()

	for _, member := range state.AllMembers() {
		testRaftServer.ApplyCommand(raft.MemberRemove, raft.DaprHostMember{
			Name:      member.Name,
			Namespace: member.Namespace,
		})
	}
}

func TestMembershipChangeWorker(t *testing.T) {
	var (
		serverAddress string
		testServer    *Service
		clock         *clocktesting.FakeClock
	)

	setupEach := func(t *testing.T) context.CancelFunc {
		ctx, cancel := context.WithCancel(context.Background())
		var cancelServer context.CancelFunc

		testRaftServer := tests.Raft(t)
		serverAddress, testServer, clock, cancelServer = newTestPlacementServer(t, testRaftServer)
		testServer.hasLeadership.Store(true)
		membershipStopCh := make(chan struct{})

		cleanupStates(testRaftServer)
		state := testRaftServer.FSM().State()
		state.Lock.RLock()
		assert.Empty(t, state.AllMembers())
		state.Lock.RUnlock()

		go func() {
			defer close(membershipStopCh)
			testServer.membershipChangeWorker(ctx)
		}()

		time.Sleep(time.Second * 2)

		return func() {
			cancel()
			cancelServer()
			select {
			case <-membershipStopCh:
			case <-time.After(time.Second):
				t.Error("membershipChangeWorker did not stop in time")
			}
		}
	}

	t.Run("successful dissemination", func(t *testing.T) {
		t.Cleanup(setupEach(t))
		conn1, _, stream1 := newTestClient(t, serverAddress)
		conn2, _, stream2 := newTestClient(t, serverAddress)
		conn3, _, stream3 := newTestClient(t, serverAddress)

		// Receive the first (empty) message and the second message that should contain
		// the full placement table
		done := make(chan bool)
		go func() {
			cnt := 0
			for {
				placementOrder, streamErr := stream1.Recv()
				require.NoError(t, streamErr)
				if placementOrder.GetOperation() == "unlock" {
					cnt++
				}
				if cnt == 2 {
					done <- true
					return
				}
			}
		}()

		host1 := &v1pb.Host{
			Name:      "127.0.0.1:50100",
			Namespace: "ns1",
			Entities:  []string{"actor1", "actor2"},
			Id:        "testAppID",
			Load:      1,
		}
		host2 := &v1pb.Host{
			Name:      "127.0.0.1:50101",
			Namespace: "ns2",
			Entities:  []string{"actor3"},
			Id:        "testAppID2",
			Load:      1,
		}
		host3 := &v1pb.Host{
			Name:      "127.0.0.1:50102",
			Namespace: "ns2",
			Entities:  []string{"actor4"},
			Id:        "testAppID3",
			Load:      1,
		}

		require.NoError(t, stream1.Send(host1))
		require.NoError(t, stream2.Send(host2))
		require.NoError(t, stream3.Send(host3))

		require.Eventually(t, func() bool {
			assert.Equal(t, 1, testServer.streamConnPool.getStreamCount("ns1"))
			assert.Equal(t, 2, testServer.streamConnPool.getStreamCount("ns2"))

			// This indicates the member has been added to the dissemination queue and is
			// going to be disseminated in the next tick
			ts1, ok := testServer.disseminateNextTime.Get("ns1")
			assert.True(t, ok)
			assert.Equal(t, clock.Now().Add(disseminateTimeout).UnixNano(), ts1.Load())

			ts2, ok := testServer.disseminateNextTime.Get("ns2")
			assert.True(t, ok)
			assert.Equal(t, clock.Now().Add(disseminateTimeout).UnixNano(), ts2.Load())
			return true
		}, 10*time.Second, 100*time.Millisecond)

		// Move the clock forward so dissemination is triggered
		clock.Step(disseminateTimeout)

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("dissemination did not happen in time")
		}

		// ignore disseminateTimeout.
		testServer.disseminateNextTime.GetOrCreate("ns1", 0).Store(0)

		// Check member has been saved correctly in the raft store
		assert.Eventually(t, func() bool {
			state := testServer.raftNode.FSM().State()
			state.Lock.RLock()
			members1, err := state.Members("ns1")

			if err != nil {
				state.Lock.RUnlock()
				return false
			}
			if len(members1) == 1 {
				m, ok := members1["127.0.0.1:50100"]
				require.True(t, ok)
				require.Equal(t, "127.0.0.1:50100", m.Name)
				require.Equal(t, "ns1", m.Namespace)
				require.Contains(t, m.Entities, "actor1", "actor2")
			}

			members2, err := state.Members("ns2")

			if err != nil {
				state.Lock.RUnlock()
				return false
			}
			if len(members2) == 2 {
				m1, ok := members2["127.0.0.1:50101"]
				require.True(t, ok)
				require.Equal(t, "127.0.0.1:50101", m1.Name)
				require.Equal(t, "ns2", m1.Namespace)
				require.Contains(t, m1.Entities, "actor3")

				m2, ok := members2["127.0.0.1:50102"]
				require.True(t, ok)
				require.Equal(t, "127.0.0.1:50102", m2.Name)
				require.Equal(t, "ns2", m2.Namespace)
				require.Contains(t, m2.Entities, "actor4")
			}
			state.Lock.RUnlock()

			return true
		}, time.Second, time.Millisecond, "the member hasn't been saved in the raft store")

		// Wait until next table dissemination and check there hasn't been updates
		clock.Step(disseminateTimerInterval)
		require.Eventually(t, func() bool {
			cnt, ok := testServer.memberUpdateCount.Get("ns1")
			if !ok {
				return false
			}
			return cnt.Load() == 0
		}, 5*time.Second, time.Millisecond, "flushed all member updates")

		conn1.Close()
		conn2.Close()
		conn3.Close()

		require.Eventually(t, func() bool {
			state := testServer.raftNode.FSM().State()
			state.Lock.RLock()
			defer state.Lock.RUnlock()
			require.Empty(t, state.AllMembers())

			// Disseminate locks have been deleted
			require.Equal(t, 0, testServer.disseminateLocks.ItemCount())

			// Disseminate timers have been deleted
			_, ok := testServer.disseminateNextTime.Get("ns1")
			require.False(t, ok)
			_, ok = testServer.disseminateNextTime.Get("ns2")
			require.False(t, ok)

			// Member update counts have been deleted
			_, ok = testServer.memberUpdateCount.Get("ns1")
			require.False(t, ok)
			_, ok = testServer.memberUpdateCount.Get("ns2")
			require.False(t, ok)

			return true
		}, 5*time.Second, 100*time.Millisecond)

	})
}

func PerformTableUpdateCostTime(t *testing.T) (wastedTime int64) {
	// Replace the logger for this test so we reduce the noise
	// prevLog := log
	// log = logger.NewLogger("dapr.placement1")
	// log.SetOutputLevel(logger.InfoLevel)
	// t.Cleanup(func() {
	// 	log = prevLog
	// } )
	// commenting out temporarily because of a race condition on log

	const testClients = 10
	serverAddress, testServer, _, cleanup := newTestPlacementServer(t, tests.Raft(t))
	testServer.hasLeadership.Store(true)
	var (
		overArr     [testClients]int64
		overArrLock sync.RWMutex
		// arrange.
		clientConns   []*grpc.ClientConn
		clientStreams []v1pb.Placement_ReportDaprStatusClient
		wg            sync.WaitGroup
	)
	startFlag := atomic.Bool{}
	startFlag.Store(false)
	wg.Add(testClients)
	for i := 0; i < testClients; i++ {
		conn, _, stream := newTestClient(t, serverAddress)
		clientConns = append(clientConns, conn)
		clientStreams = append(clientStreams, stream)
		go func(clientID int, clientStream v1pb.Placement_ReportDaprStatusClient) {
			defer wg.Done()
			var start time.Time
			for {
				placementOrder, streamErr := clientStream.Recv()
				if streamErr != nil {
					return
				}
				if placementOrder != nil {
					if placementOrder.GetOperation() == "lock" {
						if startFlag.Load() && placementOrder.GetTables() != nil && placementOrder.GetTables().GetVersion() == "demo" {
							start = time.Now()
							if clientID == 1 {
								t.Log("client 1 lock", start)
							}
						}
					}
					if placementOrder.GetOperation() == "update" {
						continue
					}
					if placementOrder.GetOperation() == "unlock" {
						if startFlag.Load() && placementOrder.GetTables() != nil && placementOrder.GetTables().GetVersion() == "demo" {
							if clientID == 1 {
								t.Log("client 1 unlock", time.Now())
							}
							overArrLock.Lock()
							overArr[clientID] = time.Since(start).Milliseconds()
							overArrLock.Unlock()
						}
					}
				}
			}
		}(i, stream)
	}

	// register
	for i := 0; i < testClients; i++ {
		host := &v1pb.Host{
			Name:     fmt.Sprintf("127.0.0.1:5010%d", i),
			Entities: []string{"DogActor", "CatActor"},
			Id:       "testAppID",
			Load:     1, // Not used yet.
		}
		require.NoError(t, clientStreams[i].Send(host))
	}

	// Wait until clientStreams[clientID].Recv() in client go routine received new table.
	ns := "" // The client didn't send any namespace
	require.Eventually(t, func() bool {
		return testServer.streamConnPool.getStreamCount(ns) == testClients
	}, 15*time.Second, 100*time.Millisecond)

	streamConnPool := make([]daprdStream, 0)
	testServer.streamConnPool.forEachInNamespace(ns, func(streamId uint32, val *daprdStream) {
		streamConnPool = append(streamConnPool, *val)
	})

	startFlag.Store(true)
	mockMessage := &v1pb.PlacementTables{Version: "demo"}

	for _, host := range streamConnPool {
		require.NoError(t, testServer.disseminateOperation(context.Background(), host, "lock", mockMessage))
		require.NoError(t, testServer.disseminateOperation(context.Background(), host, "update", mockMessage))
		require.NoError(t, testServer.disseminateOperation(context.Background(), host, "unlock", mockMessage))
	}
	startFlag.Store(false)
	var max int64
	overArrLock.RLock()
	for i := range overArr {
		if overArr[i] > max {
			max = overArr[i]
		}
	}
	overArrLock.RUnlock()

	// clean up resources.
	for i := 0; i < testClients; i++ {
		require.NoError(t, clientConns[i].Close())
	}
	cleanup()
	return max
}

func TestPerformTableUpdatePerf(t *testing.T) {
	for i := 0; i < 3; i++ {
		fmt.Println("max cost time(ms)", PerformTableUpdateCostTime(t))
	}
}

// MockPlacementGRPCStream simulates the behavior of placementv1pb.Placement_ReportDaprStatusServer
type MockPlacementGRPCStream struct {
	v1pb.Placement_ReportDaprStatusServer
	ctx context.Context
}

func (m MockPlacementGRPCStream) Context() context.Context {
	return m.ctx
}

// Utility function to create metadata and context for testing
func createContextWithMetadata(key, value string) context.Context {
	md := metadata.Pairs(key, value)
	return metadata.NewIncomingContext(context.Background(), md)
}

func TestExpectsVNodes(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
		expected bool
	}{
		{
			name:     "Without metadata",
			ctx:      context.Background(),
			expected: true,
		},
		{
			name:     "With metadata expectsVNodes true",
			ctx:      createContextWithMetadata(GRPCContextKeyAcceptVNodes, "true"),
			expected: true,
		},
		{
			name:     "With metadata expectsVNodes false",
			ctx:      createContextWithMetadata(GRPCContextKeyAcceptVNodes, "false"),
			expected: false,
		},
		{
			name:     "With invalid metadata value",
			ctx:      createContextWithMetadata(GRPCContextKeyAcceptVNodes, "invalid"),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := MockPlacementGRPCStream{ctx: tt.ctx}
			result := hostNeedsVNodes(stream)
			if result != tt.expected {
				t.Errorf("expectsVNodes() for %s: expected %v, got %v", tt.name, tt.expected, result)
			}
		})
	}
}
