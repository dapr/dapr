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

	state.ForEachHost(func(host *raft.DaprHostMember) bool {
		testRaftServer.ApplyCommand(raft.MemberRemove, raft.DaprHostMember{
			Name:      host.Name,
			Namespace: host.Namespace,
		})
		return true
	})
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
		require.Equal(t, 0, state.MemberCount())

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

		ch := make(chan struct{}, 3)

		var operations1 []string
		go func() {
			require.EventuallyWithT(t, func(c *assert.CollectT) {
				placementOrder, streamErr := stream1.Recv()
				//nolint:testifylint
				assert.NoError(c, streamErr)
				if placementOrder.GetOperation() == lockOperation {
					operations1 = append(operations1, lockOperation)
				}
				if placementOrder.GetOperation() == updateOperation {
					tables := placementOrder.GetTables()
					assert.Len(c, tables.GetEntries(), 2)
					assert.Contains(c, tables.GetEntries(), "actor1")
					assert.Contains(c, tables.GetEntries(), "actor2")
					operations1 = append(operations1, updateOperation)
				}
				if placementOrder.GetOperation() == unlockOperation {
					operations1 = append(operations1, unlockOperation)
				}

				if assert.Len(c, operations1, 3) {
					require.Equal(c, lockOperation, operations1[0])
					require.Equal(c, updateOperation, operations1[1])
					require.Equal(c, unlockOperation, operations1[2])
				}
			}, 20*time.Second, 100*time.Millisecond)
			ch <- struct{}{}
		}()

		var operations2 []string
		go func() {
			require.EventuallyWithT(t, func(c *assert.CollectT) {
				placementOrder, streamErr := stream2.Recv()
				//nolint:testifylint
				assert.NoError(c, streamErr)
				if placementOrder.GetOperation() == lockOperation {
					operations2 = append(operations2, lockOperation)
				}
				if placementOrder.GetOperation() == updateOperation {
					entries := placementOrder.GetTables().GetEntries()
					if !assert.Len(c, entries, 2) {
						return
					}
					if !assert.Contains(c, entries, "actor3") {
						return
					}
					if !assert.Contains(c, entries, "actor4") {
						return
					}
					operations2 = append(operations2, updateOperation)
				}
				if placementOrder.GetOperation() == unlockOperation {
					operations2 = append(operations2, unlockOperation)
				}

				// Depending on the timing of the host 3 registration
				// we may receive one or two update messages
				assert.GreaterOrEqual(c, len(operations2), 3)
			}, 20*time.Second, 100*time.Millisecond)
			ch <- struct{}{}
		}()

		var operations3 []string
		go func() {
			require.EventuallyWithT(t, func(c *assert.CollectT) {
				placementOrder, streamErr := stream3.Recv()
				//nolint:testifylint
				assert.NoError(c, streamErr)
				if placementOrder.GetOperation() == lockOperation {
					operations3 = append(operations3, lockOperation)
				}
				if placementOrder.GetOperation() == updateOperation {
					entries := placementOrder.GetTables().GetEntries()
					if !assert.Len(c, entries, 2) {
						return
					}
					if !assert.Contains(c, entries, "actor3") {
						return
					}
					if !assert.Contains(c, entries, "actor4") {
						return
					}
					operations3 = append(operations3, updateOperation)
				}
				if placementOrder.GetOperation() == unlockOperation {
					operations3 = append(operations3, unlockOperation)
				}

				// Depending on the timing of the host 3 registration
				// we may receive one or two update messages
				if assert.GreaterOrEqual(c, len(operations3), 3) {
					require.Equal(c, lockOperation, operations3[0])
					require.Equal(c, updateOperation, operations3[1])
					require.Equal(c, unlockOperation, operations3[2])
				}
			}, 20*time.Second, 100*time.Millisecond)
			ch <- struct{}{}
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

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Equal(c, 1, testServer.streamConnPool.getStreamCount("ns1"))
			assert.Equal(c, 2, testServer.streamConnPool.getStreamCount("ns2"))
			assert.Equal(c, uint32(3), testServer.streamConnPool.streamIndex.Load())
			assert.Len(c, testServer.streamConnPool.reverseLookup, 3)

			// This indicates the member has been added to the dissemination queue and is
			// going to be disseminated in the next tick
			ts1, ok := testServer.disseminateNextTime.Get("ns1")
			if assert.True(c, ok) {
				assert.Equal(c, clock.Now().Add(disseminateTimeout).UnixNano(), ts1.Load())
			}

			ts2, ok := testServer.disseminateNextTime.Get("ns2")
			if assert.True(c, ok) {
				assert.Equal(c, clock.Now().Add(disseminateTimeout).UnixNano(), ts2.Load())
			}
		}, 10*time.Second, 100*time.Millisecond)

		// Move the clock forward so dissemination is triggered
		clock.Step(disseminateTimeout)

		// Wait for all three hosts to receive the updates
		updateMsgCnt := 0
		require.Eventually(t, func() bool {
			select {
			case <-ch:
				updateMsgCnt++
			}
			return updateMsgCnt == 3
		}, 20*time.Second, 100*time.Millisecond)

		// Ignore the next disseminateTimeout.
		val, _ := testServer.disseminateNextTime.GetOrSet("ns1", &atomic.Int64{})
		val.Store(0)

		// Check members has been saved correctly in the raft store
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			state := testServer.raftNode.FSM().State()
			cnt := 0
			state.ForEachHostInNamespace("ns1", func(host *raft.DaprHostMember) bool {
				assert.Equal(c, "127.0.0.1:50100", host.Name)
				assert.Equal(c, "ns1", host.Namespace)
				assert.Contains(c, host.Entities, "actor1", "actor2")
				cnt++
				return true
			})
			assert.Equal(t, 1, cnt)

			cnt = 0
			state.ForEachHostInNamespace("ns2", func(host *raft.DaprHostMember) bool {
				if host.Name == "127.0.0.1:50101" {
					assert.Equal(c, "127.0.0.1:50101", host.Name)
					assert.Equal(c, "ns2", host.Namespace)
					assert.Contains(c, host.Entities, "actor3")
					cnt++
				}

				if host.Name == "127.0.0.1:50102" {
					assert.Equal(c, "127.0.0.1:50102", host.Name)
					assert.Equal(c, "ns2", host.Namespace)
					assert.Contains(c, host.Entities, "actor4")
					cnt++
				}

				return true
			})
			assert.Equal(t, 2, cnt)
		}, 10*time.Second, time.Millisecond, "the member hasn't been saved in the raft store")

		// Wait until next table dissemination and check there haven't been any updates
		clock.Step(disseminateTimerInterval)
		require.Eventually(t, func() bool {
			cnt, ok := testServer.memberUpdateCount.Get("ns1")
			if !ok {
				return false
			}
			return cnt.Load() == 0
		}, 10*time.Second, time.Millisecond, "flushed all member updates")

		// Disconnect the host in ns1
		conn1.Close()
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Equal(c, 2, testServer.raftNode.FSM().State().MemberCount())

			// Disseminate locks have been deleted for ns1, but not ns2
			assert.Equal(c, 1, testServer.disseminateLocks.ItemCount())

			// Disseminate timers have been deleted for ns1, but not ns2
			_, ok := testServer.disseminateNextTime.Get("ns1")
			assert.False(c, ok)
			_, ok = testServer.disseminateNextTime.Get("ns2")
			assert.True(c, ok)

			// Member update counts have been deleted for ns1, but not ns2
			_, ok = testServer.memberUpdateCount.Get("ns1")
			assert.False(c, ok)
			_, ok = testServer.memberUpdateCount.Get("ns2")
			assert.True(c, ok)

			assert.Equal(c, 0, testServer.streamConnPool.getStreamCount("ns1"))
			assert.Equal(c, 2, testServer.streamConnPool.getStreamCount("ns2"))
			assert.Equal(c, uint32(3), testServer.streamConnPool.streamIndex.Load())
			testServer.streamConnPool.lock.RLock()
			assert.Len(c, testServer.streamConnPool.reverseLookup, 2)
			testServer.streamConnPool.lock.RUnlock()
		}, 20*time.Second, 100*time.Millisecond)

		// // Disconnect one host in ns2
		conn2.Close()
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Equal(c, 1, testServer.raftNode.FSM().State().MemberCount())

			// Disseminate lock for ns2 hasn't been deleted
			assert.Equal(c, 1, testServer.disseminateLocks.ItemCount())

			// Disseminate timer for ns2 hasn't been deleted,
			// because there's still streams in the namespace
			_, ok := testServer.disseminateNextTime.Get("ns1")
			assert.False(c, ok)
			_, ok = testServer.disseminateNextTime.Get("ns2")
			assert.True(c, ok)

			// Member update count for ns2 hasn't been deleted,
			// because there's still streams in the namespace
			_, ok = testServer.memberUpdateCount.Get("ns2")
			assert.True(c, ok)

			assert.Equal(c, 0, testServer.streamConnPool.getStreamCount("ns1"))
			assert.Equal(c, 1, testServer.streamConnPool.getStreamCount("ns2"))
			assert.Equal(c, uint32(3), testServer.streamConnPool.streamIndex.Load())
			testServer.streamConnPool.lock.RLock()
			assert.Len(c, testServer.streamConnPool.reverseLookup, 1)
			testServer.streamConnPool.lock.RUnlock()
		}, 20*time.Second, 100*time.Millisecond)

		// Last host is disconnected
		conn3.Close()
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Equal(c, 0, testServer.raftNode.FSM().State().MemberCount())

			// Disseminate locks have been deleted
			assert.Equal(c, 0, testServer.disseminateLocks.ItemCount())

			// Disseminate timers have been deleted
			_, ok := testServer.disseminateNextTime.Get("ns1")
			assert.False(c, ok)
			_, ok = testServer.disseminateNextTime.Get("ns2")
			assert.False(c, ok)

			// Member update counts have been deleted
			_, ok = testServer.memberUpdateCount.Get("ns1")
			assert.False(c, ok)
			_, ok = testServer.memberUpdateCount.Get("ns2")
			assert.False(c, ok)

			assert.Equal(c, 0, testServer.streamConnPool.getStreamCount("ns1"))
			assert.Equal(c, 0, testServer.streamConnPool.getStreamCount("ns2"))
			testServer.streamConnPool.lock.RLock()
			assert.Empty(c, testServer.streamConnPool.reverseLookup)
			testServer.streamConnPool.lock.RUnlock()
		}, 20*time.Second, 100*time.Millisecond)
	})
}

func PerformTableUpdateCostTime(t *testing.T) (wastedTime int64) {
	const testClients = 10
	serverAddress, testServer, _, cleanup := newTestPlacementServer(t, tests.Raft(t))
	testServer.hasLeadership.Store(true)
	var (
		overArr       [testClients]int64
		overArrLock   sync.RWMutex
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
					if placementOrder.GetOperation() == lockOperation {
						if startFlag.Load() {
							start = time.Now()
							if clientID == 1 {
								t.Log("client 1 lock", start)
							}
						}
					}
					if placementOrder.GetOperation() == updateOperation {
						continue
					}
					if placementOrder.GetOperation() == unlockOperation {
						if startFlag.Load() {
							if clientID == 1 {
								t.Log("client 1 unlock", time.Now())
							}
							overArrLock.Lock()
							overArr[clientID] = time.Since(start).Nanoseconds()
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
		require.NoError(t, testServer.disseminateOperation(context.Background(), host, lockOperation, mockMessage))
		require.NoError(t, testServer.disseminateOperation(context.Background(), host, updateOperation, mockMessage))
		require.NoError(t, testServer.disseminateOperation(context.Background(), host, unlockOperation, mockMessage))
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
		fmt.Println("max cost time(ns)", PerformTableUpdateCostTime(t))
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
