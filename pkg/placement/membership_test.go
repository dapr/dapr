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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
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

	raftOpts, err := tests.RaftOpts(t)
	require.NoError(t, err)

	setupEach := func(t *testing.T) context.CancelFunc {
		ctx, cancel := context.WithCancel(t.Context())
		var cancelServer context.CancelFunc

		serverAddress, testServer, clock, cancelServer = newTestPlacementServer(t, *raftOpts)
		testServer.hasLeadership.Store(true)
		membershipStopCh := make(chan struct{})

		cleanupStates(testServer.raftNode)
		state := testServer.raftNode.FSM().State()
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

		go func() {
			assert.EventuallyWithT(t, func(c *assert.CollectT) {
				placementOrder, streamErr := stream1.Recv()
				assert.NoError(c, streamErr)

				if placementOrder.GetOperation() == updateOperation {
					tables := placementOrder.GetTables()
					if assert.Len(c, tables.GetEntries(), 2) &&
						assert.Contains(c, tables.GetEntries(), "actor1") &&
						assert.Contains(c, tables.GetEntries(), "actor2") {
						return
					}
				}
				assert.Fail(c, "didn't receive updates in time")
			}, 20*time.Second, 100*time.Millisecond)
			ch <- struct{}{}
		}()

		var operations2 []string
		go func() {
			assert.EventuallyWithT(t, func(c *assert.CollectT) {
				placementOrder, streamErr := stream2.Recv()
				assert.NoError(c, streamErr)
				if placementOrder.GetOperation() == lockOperation {
					operations2 = append(operations2, lockOperation)
				}
				if placementOrder.GetOperation() == updateOperation {
					entries := placementOrder.GetTables().GetEntries()
					if assert.Len(c, entries, 2) &&
						assert.Contains(c, entries, "actor3") &&
						assert.Contains(c, entries, "actor4") {
						return
					}
				}

				assert.Fail(c, "didn't receive updates in time")
			}, 20*time.Second, 100*time.Millisecond)
			ch <- struct{}{}
		}()

		go func() {
			assert.EventuallyWithT(t, func(c *assert.CollectT) {
				placementOrder, streamErr := stream3.Recv()
				assert.NoError(c, streamErr)

				if placementOrder.GetOperation() == updateOperation {
					entries := placementOrder.GetTables().GetEntries()
					if assert.Len(c, entries, 2) &&
						assert.Contains(c, entries, "actor3") &&
						assert.Contains(c, entries, "actor4") {
						return
					}
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
		}, 10*time.Second, 100*time.Millisecond)

		// Move the clock forward so dissemination is triggered
		clock.Step(testServer.disseminateTimeout)

		// Wait for all three hosts to receive the updates
		updateMsgCnt := 0
		require.Eventually(t, func() bool {
			select {
			case <-ch:
				updateMsgCnt++
			}
			return updateMsgCnt == 3
		}, 20*time.Second, 100*time.Millisecond)

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
		clock.Step(time.Second)
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

			// Member update counts have been deleted for ns1, but not ns2
			_, ok := testServer.memberUpdateCount.Get("ns1")
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

			// Member update count for ns2 hasn't been deleted,
			// because there's still streams in the namespace
			_, ok := testServer.memberUpdateCount.Get("ns2")
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

			// Member update counts have been deleted
			_, ok := testServer.memberUpdateCount.Get("ns1")
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

	t.Run("actors can be unregistered", func(t *testing.T) {
		t.Cleanup(setupEach(t))
		conn1, _, stream1 := newTestClient(t, serverAddress)
		conn2, _, stream2 := newTestClient(t, serverAddress)

		t.Cleanup(func() {
			conn1.Close()
			conn2.Close()
		})

		ch := make(chan struct{})

		go func() {
			assert.EventuallyWithT(t, func(c *assert.CollectT) {
				placementOrder, streamErr := stream2.Recv()
				assert.NoError(c, streamErr)

				if placementOrder.GetOperation() == updateOperation {
					tables := placementOrder.GetTables()
					if assert.Len(c, tables.GetEntries(), 2) &&
						assert.Contains(c, tables.GetEntries(), "actor1") &&
						assert.Contains(c, tables.GetEntries(), "actor2") {
						return
					}
				}
			}, 10*time.Second, 100*time.Millisecond)
			ch <- struct{}{}
		}()

		host1 := &v1pb.Host{
			Name:      "127.0.0.1:50100",
			Namespace: "ns1",
			Entities:  []string{"actor1", "actor2"},
			Id:        "testAppID1",
			Load:      1,
		}
		host2 := &v1pb.Host{
			Name:      "127.0.0.1:50101",
			Namespace: "ns1",
			Entities:  []string{},
			Id:        "testAppID2",
			Load:      1,
		}
		require.NoError(t, stream1.Send(host1))
		require.NoError(t, stream2.Send(host2))

		// Move the clock forward so dissemination is triggered
		clock.Step(testServer.disseminateTimeout)

		// Wait for the second host to receive the updates
		require.Eventually(t, func() bool {
			select {
			case <-ch:
				return true
			case <-time.After(10 * time.Second):
				return false
			}
		}, 10*time.Second, 100*time.Millisecond)

		// Check members have been correctly saved in the raft store
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
		}, 10*time.Second, time.Millisecond, "the member hasn't been saved in the raft store")

		// Unregister actors in the actor host
		go func() {
			assert.EventuallyWithT(t, func(c *assert.CollectT) {
				placementOrder, streamErr := stream2.Recv()
				assert.NoError(c, streamErr)

				if placementOrder.GetOperation() == updateOperation {
					tables := placementOrder.GetTables()
					if assert.Empty(c, tables.GetEntries()) {
						return
					}
				}
			}, 20*time.Second, 100*time.Millisecond)
			ch <- struct{}{}
		}()

		host1 = &v1pb.Host{
			Name:      "127.0.0.1:50100",
			Namespace: "ns1",
			Entities:  []string{},
			Id:        "testAppID1",
			Load:      1,
		}
		host2 = &v1pb.Host{
			Name:      "127.0.0.1:50101",
			Namespace: "ns2",
			Entities:  []string{},
			Id:        "testAppID2",
			Load:      1,
		}
		require.NoError(t, stream1.Send(host1))
		require.NoError(t, stream2.Send(host2))

		// Check member has been correctly removed from the raft store
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			state := testServer.raftNode.FSM().State()
			assert.Equal(c, 0, state.MemberCountInNamespace("ns1"))
		}, 10*time.Second, time.Millisecond, "the member hasn't been removed from the raft store")
	})
}

func PerformTableUpdateCostTime(t *testing.T) (wastedTime int64) {
	const testClients = 10

	raftOpts, err := tests.RaftOpts(t)
	require.NoError(t, err)

	serverAddress, testServer, _, cleanup := newTestPlacementServer(t, *raftOpts)
	testServer.hasLeadership.Store(true)
	var (
		disseminationTime [testClients]int64
		clientConns       = make([]*grpc.ClientConn, 0, testClients)
		clientStreams     = make([]v1pb.Placement_ReportDaprStatusClient, 0, testClients)
		wg                sync.WaitGroup
	)

	// Create test clients to read from the streams
	for i := range testClients {
		conn, _, stream := newTestClient(t, serverAddress)
		clientConns = append(clientConns, conn)
		clientStreams = append(clientStreams, stream)
		wg.Add(1)
		go func(clientID int, clientStream v1pb.Placement_ReportDaprStatusClient) {
			defer wg.Done()
			for {
				_, streamErr := clientStream.Recv()
				if streamErr != nil {
					return
				}
			}
		}(i, stream)
	}

	// Clients register actor types with placement
	for i := range testClients {
		host := &v1pb.Host{
			Name:     fmt.Sprintf("127.0.0.1:5010%d", i),
			Entities: []string{"DogActor", "CatActor"},
			Id:       "testAppID",
			Load:     1, // Not used yet.
		}
		require.NoError(t, clientStreams[i].Send(host))
	}

	// Wait until all clientStreams received the new table and got registered with placement
	ns := "" // The client didn't send any namespace
	require.Eventually(t, func() bool {
		return testServer.streamConnPool.getStreamCount(ns) == testClients
	}, 15*time.Second, 100*time.Millisecond)

	streamConnPool := make([]daprdStream, 0)
	testServer.streamConnPool.forEachInNamespace(ns, func(streamId uint32, val *daprdStream) {
		streamConnPool = append(streamConnPool, *val)
	})

	mockMessage := &v1pb.PlacementTables{Version: "demo"}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	var start time.Time
	for i, host := range streamConnPool {
		start = time.Now()
		require.NoError(t, testServer.disseminateOperation(ctx, host, lockOperation, mockMessage))
		require.NoError(t, testServer.disseminateOperation(ctx, host, updateOperation, mockMessage))
		require.NoError(t, testServer.disseminateOperation(ctx, host, unlockOperation, mockMessage))
		disseminationTime[i] = time.Since(start).Nanoseconds()
	}

	var max int64
	for i := range disseminationTime {
		fmt.Println("client ", i, " cost time(ns): ", disseminationTime[i])
		if disseminationTime[i] > max {
			max = disseminationTime[i]
		}
	}

	// Close streams and tcp connections
	for i := range testClients {
		require.NoError(t, clientStreams[i].CloseSend())
		require.NoError(t, clientConns[i].Close())
	}
	wg.Wait()
	cleanup()
	return max
}

func TestPerformTableUpdatePerf(t *testing.T) {
	for range 3 {
		fmt.Printf("==== Max cost time %d ms", PerformTableUpdateCostTime(t)/1000)
	}
}
