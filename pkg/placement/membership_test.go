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
	"strconv"
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
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/kit/logger"
)

func cleanupStates() {
	testRaftServer.FSM().State().Lock.RLock()
	defer testRaftServer.FSM().State().Lock.RUnlock()

	for _, member := range testRaftServer.FSM().State().AllMembers() {
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
		serverAddress, testServer, clock, cancelServer = newTestPlacementServer(t, testRaftServer)
		testServer.hasLeadership.Store(true)
		membershipStopCh := make(chan struct{})

		cleanupStates()
		testRaftServer.FSM().State().Lock.RLock()
		assert.Empty(t, testServer.raftNode.FSM().State().AllMembers())
		testRaftServer.FSM().State().Lock.RUnlock()

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

	arrangeFakeMembers := func(t *testing.T) {
		for i := 0; i < 3; i++ {
			testServer.membershipCh <- hostMemberChange{
				cmdType: raft.MemberUpsert,
				host: raft.DaprHostMember{
					Name:      fmt.Sprintf("127.0.0.1:%d", 50000+i),
					Namespace: "ns" + strconv.Itoa(i),
					AppID:     fmt.Sprintf("TestAPPID%d", 50000+i),
					Entities:  []string{"actorTypeOne", "actorTypeTwo"},
				},
			}
		}

		// wait until all host member change requests are flushed
		require.Eventually(t, func() bool {
			testServer.raftNode.FSM().State().Lock.RLock()
			cntMembers := testServer.raftNode.FSM().State().AllMembers()
			testServer.raftNode.FSM().State().Lock.RUnlock()
			return len(cntMembers) == 3
		}, disseminateTimerInterval+time.Second*3, time.Millisecond)
	}

	t.Run("successful dissemination", func(t *testing.T) {
		t.Cleanup(setupEach(t))
		// arrange
		testServer.faultyHostDetectDuration.Store(int64(faultyHostDetectInitialDuration))

		conn, _, stream1 := newTestClient(t, serverAddress)
		conn, _, stream2 := newTestClient(t, serverAddress)
		conn, _, stream3 := newTestClient(t, serverAddress)

		done := make(chan bool)
		cnt := 0
		go func() {
			for {
				placementOrder, streamErr := stream1.Recv()
				require.NoError(t, streamErr)
				if placementOrder.GetOperation() == "unlock" {
					cnt++
				}
				placementOrder, streamErr = stream2.Recv()
				require.NoError(t, streamErr)
				if placementOrder.GetOperation() == "unlock" {
					cnt++
				}
				placementOrder, streamErr = stream3.Recv()
				require.NoError(t, streamErr)
				if placementOrder.GetOperation() == "unlock" {
					cnt++
				}
				if cnt == 3 {
					done <- true
					return
				}
			}
		}()

		host1 := &v1pb.Host{
			Name:      "127.0.0.1:50100",
			Namespace: "ns1",
			Entities:  []string{"DogActor", "CatActor"},
			Id:        "testAppID1",
			Load:      1,
		}
		host2 := &v1pb.Host{
			Name:      "127.0.0.1:50101",
			Namespace: "ns2",
			Entities:  []string{"DogActor", "CatActor", "IguanaActor"},
			Id:        "testAppID2",
			Load:      1,
		}
		host3 := &v1pb.Host{
			Name:      "127.0.0.1:50102",
			Namespace: "ns2",
			Entities:  []string{"DogActor", "CatActor", "IguanaActor"},
			Id:        "testAppID3",
			Load:      1,
		}

		// act
		require.NoError(t, stream1.Send(host1))
		require.NoError(t, stream2.Send(host2))
		require.NoError(t, stream3.Send(host3))

		select {
		case <-done:
		case <-time.After(disseminateTimerInterval + time.Second*5):
			t.Error("dissemination did not happen in time")
		}

		// ignore disseminateTimeout.
		testServer.disseminateNextTime.GetOrCreate("ns1", 0).Store(0)

		nStreamConnPool1 := testServer.streamConnPool.getStreamCount("ns1")
		require.Equal(t, 1, nStreamConnPool1)

		nStreamConnPool2 := testServer.streamConnPool.getStreamCount("ns2")
		require.Equal(t, 2, nStreamConnPool2)

		assert.Eventually(t, func() bool {
			testServer.raftNode.FSM().State().Lock.RLock()
			m1, err := testServer.raftNode.FSM().State().Members("ns1")
			if err != nil {
				return false
			}
			testServer.raftNode.FSM().State().Lock.RUnlock()

			condition1 := nStreamConnPool1 == len(m1)

			// You can use asserts here for logging purposes
			assert.True(t, condition1)

			return condition1
		}, time.Second, time.Millisecond)

		// wait until table dissemination.
		require.Eventually(t, func() bool {
			clock.Step(disseminateTimerInterval)
			return testServer.memberUpdateCount.GetOrCreate("ns1", 0).Load() == 0 &&
				testServer.memberUpdateCount.GetOrCreate("ns2", 0).Load() == 0 &&
				testServer.faultyHostDetectDuration.Load() == int64(faultyHostDetectDefaultDuration)
		}, time.Second*5, time.Millisecond,
			"flushed all member updates and faultyHostDetectDuration must be faultyHostDetectDuration")

		conn.Close()
		time.Sleep(time.Second * 3)
	})

	t.Run("faulty host detector", func(t *testing.T) {
		// arrange
		t.Cleanup(setupEach(t))
		arrangeFakeMembers(t)

		// faulty host detector removes all members if heartbeat does not happen
		assert.Eventually(t, func() bool {
			clock.Step(disseminateTimerInterval)
			testServer.raftNode.FSM().State().Lock.RLock()
			defer testServer.raftNode.FSM().State().Lock.RUnlock()
			return len(testServer.raftNode.FSM().State().AllMembers()) == 0
		}, 5*time.Second, time.Millisecond)
	})
}

func TestPerformTableUpdate(t *testing.T) {
	const testClients = 10
	serverAddress, testServer, clock, cleanup := newTestPlacementServer(t, testRaftServer)
	testServer.hasLeadership.Store(true)

	// arrange
	clientConns := []*grpc.ClientConn{}
	clientStreams := []v1pb.Placement_ReportDaprStatusClient{}
	clientRecvDataLock := &sync.RWMutex{}
	clientRecvData := []map[string]int64{}
	clientUpToDateCh := make(chan struct{}, testClients)

	for i := 0; i < testClients; i++ {
		conn, _, stream := newTestClient(t, serverAddress)
		clientConns = append(clientConns, conn)
		clientStreams = append(clientStreams, stream)
		clientRecvData = append(clientRecvData, map[string]int64{})

		go func(clientID int, clientStream v1pb.Placement_ReportDaprStatusClient) {
			upToDate := false
			for {
				placementOrder, streamErr := clientStream.Recv()
				if streamErr != nil {
					return
				}
				if placementOrder != nil {
					clientRecvDataLock.Lock()
					clientRecvData[clientID][placementOrder.GetOperation()] = clock.Now().UnixNano()
					clientRecvDataLock.Unlock()
					// Check if the table is up to date.
					if placementOrder.GetOperation() == "update" {
						if placementOrder.GetTables() != nil {
							upToDate = true
							for _, entries := range placementOrder.GetTables().GetEntries() {
								// Check if all clients are in load map.
								if len(entries.GetLoadMap()) != testClients {
									upToDate = false
								}
							}
						}
					}
					if placementOrder.GetOperation() == "unlock" {
						if upToDate {
							clientUpToDateCh <- struct{}{}
							return
						}
					}
				}
			}
		}(i, stream)
	}

	// act
	for i := 0; i < testClients; i++ {
		host := &v1pb.Host{
			Name:      fmt.Sprintf("127.0.0.1:5010%d", i),
			Namespace: "ns1",
			Entities:  []string{"DogActor", "CatActor", fmt.Sprintf("127.0.0.1:5010%d", i)},
			Id:        "testAppID",
			Load:      1, // Not used yet
			// Port is redundant because Name should include port number
		}

		// act
		require.NoError(t, clientStreams[i].Send(host))
	}

	// Wait until clientStreams[clientID].Recv() in client go routine received new table
	waitCnt := testClients
	timeoutC := time.After(time.Second * 10)
	for {
		end := false
		select {
		case <-clientUpToDateCh:
			waitCnt--
			if waitCnt == 0 {
				end = true
			}
		case <-timeoutC:
			end = true
		}
		if end {
			break
		}
	}

	// Call performTableUpdate directly, not by MembershipChangeWorker loop.
	streamConnPool := make([]daprdStream, 0)
	testServer.streamConnPool.forEach("ns1", func(streamId uint32, val *daprdStream) {
		streamConnPool = append(streamConnPool, *val)

	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req := &tablesUpdateRequest{hosts: streamConnPool}
	require.NoError(t, testServer.performTablesUpdate(ctx, req))

	// assert
	for i := 0; i < testClients; i++ {
		clientRecvDataLock.RLock()
		lockTime, ok := clientRecvData[i]["lock"]
		clientRecvDataLock.RUnlock()
		assert.True(t, ok)
		clientRecvDataLock.RLock()
		updateTime, ok := clientRecvData[i]["update"]
		clientRecvDataLock.RUnlock()
		assert.True(t, ok)
		clientRecvDataLock.RLock()
		unlockTime, ok := clientRecvData[i]["unlock"]
		clientRecvDataLock.RUnlock()
		assert.True(t, ok)

		// check if lock, update, and unlock operations are received in the right order.
		assert.True(t, lockTime <= updateTime && updateTime <= unlockTime)
	}

	// clean up resources
	for i := 0; i < testClients; i++ {
		clientStreams[i].CloseSend()
		clientConns[i].Close()
	}

	cleanup()
}

func PerformTableUpdateCostTime(t *testing.T) (wastedTime int64) {
	// Replace the logger for this test so we reduce the noise
	prevLog := log
	log = logger.NewLogger("dapr.placement")
	log.SetOutputLevel(logger.InfoLevel)
	t.Cleanup(func() {
		log = prevLog
	})

	const testClients = 100
	serverAddress, testServer, _, cleanup := newTestPlacementServer(t, testRaftServer)
	testServer.hasLeadership.Store(true)
	var (
		overArr [testClients]int64
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
							overArr[clientID] = time.Since(start).Milliseconds()
						}
					}
				}
			}
		}(i, stream)
	}

	// register
	for i := 0; i < testClients; i++ {
		host := &v1pb.Host{
			Name:      fmt.Sprintf("127.0.0.1:5010%d", i),
			Namespace: "ns1",
			Entities:  []string{"DogActor", "CatActor"},
			Id:        "testAppID",
			Load:      1, // Not used yet.
		}
		require.NoError(t, clientStreams[i].Send(host))
	}

	// Wait until clientStreams[clientID].Recv() in client go routine received new table.
	require.Eventually(t, func() bool {
		return testServer.streamConnPool.getStreamCount("ns1") == testClients
	}, 15*time.Second, 100*time.Millisecond)

	streamConnPool := make([]daprdStream, 0)
	testServer.streamConnPool.forEach("ns1", func(streamId uint32, val *daprdStream) {
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
	for i := range overArr {
		if overArr[i] > max {
			max = overArr[i]
		}
	}
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
