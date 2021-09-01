// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package placement

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"

	"github.com/dapr/dapr/pkg/placement/raft"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

func cleanupStates() {
	for k := range testRaftServer.FSM().State().Members() {
		testRaftServer.ApplyCommand(raft.MemberRemove, raft.DaprHostMember{
			Name: k,
		})
	}
}

func TestMembershipChangeWorker(t *testing.T) {
	serverAddress, testServer, cleanupServer := newTestPlacementServer(testRaftServer)
	testServer.hasLeadership.Store(true)

	var stopCh chan struct{}
	setupEach := func(t *testing.T) {
		cleanupStates()
		assert.Equal(t, 0, len(testServer.raftNode.FSM().State().Members()))

		stopCh = make(chan struct{})
		go testServer.membershipChangeWorker(stopCh)
	}

	arrangeFakeMembers := func(t *testing.T) {
		for i := 0; i < 3; i++ {
			testServer.membershipCh <- hostMemberChange{
				cmdType: raft.MemberUpsert,
				host: raft.DaprHostMember{
					Name:     fmt.Sprintf("127.0.0.1:%d", 50000+i),
					AppID:    fmt.Sprintf("TestAPPID%d", 50000+i),
					Entities: []string{"actorTypeOne", "actorTypeTwo"},
				},
			}
		}

		// wait until all host member change requests are flushed
		time.Sleep(disseminateTimerInterval + 10*time.Millisecond)
		assert.Equal(t, 3, len(testServer.raftNode.FSM().State().Members()))
	}

	tearDownEach := func() {
		close(stopCh)
	}

	t.Run("successful dissemination", func(t *testing.T) {
		setupEach(t)
		// arrange
		testServer.faultyHostDetectDuration.Store(int64(faultyHostDetectInitialDuration))

		conn, stream, err := newTestClient(serverAddress)
		assert.NoError(t, err)
		done := make(chan bool)
		go func() {
			for {
				placementOrder, streamErr := stream.Recv()
				if streamErr != nil {
					return
				}
				if placementOrder.Operation != "unlock" {
					done <- true
				}
			}
		}()

		host := &v1pb.Host{
			Name:     "127.0.0.1:50100",
			Entities: []string{"DogActor", "CatActor"},
			Id:       "testAppID",
			Load:     1, // Not used yet
			// Port is redundant because Name should include port number
		}

		// act
		err = stream.Send(host)
		assert.NoError(t, err)

		// wait until table dissemination.
		time.Sleep(disseminateTimerInterval)

		// ignore disseminateTimeout.
		testServer.disseminateNextTime.Store(0)

		<-done

		testServer.streamConnPoolLock.RLock()
		nStreamConnPool := len(testServer.streamConnPool)
		testServer.streamConnPoolLock.RUnlock()
		assert.Equal(t, 1, nStreamConnPool)
		assert.Equal(t, nStreamConnPool, len(testServer.raftNode.FSM().State().Members()))

		// wait until table dissemination.
		time.Sleep(disseminateTimerInterval * 2)
		assert.Equal(t, uint32(0), testServer.memberUpdateCount.Load(),
			"flushed all member updates")
		assert.Equal(t, int64(faultyHostDetectDefaultDuration), testServer.faultyHostDetectDuration.Load(),
			"faultyHostDetectDuration must be faultyHostDetectDuration")

		conn.Close()

		tearDownEach()
	})

	t.Run("faulty host detector", func(t *testing.T) {
		// arrange
		setupEach(t)
		arrangeFakeMembers(t)

		// faulty host detector removes all members if heartbeat does not happen
		time.Sleep(faultyHostDetectInitialDuration + 10*time.Millisecond)
		assert.Equal(t, 0, len(testServer.raftNode.FSM().State().Members()))

		tearDownEach()
	})

	cleanupServer()
}

func TestCleanupHeartBeats(t *testing.T) {
	_, testServer, cleanup := newTestPlacementServer(testRaftServer)
	testServer.hasLeadership.Store(true)
	maxClients := 3

	for i := 0; i < maxClients; i++ {
		testServer.lastHeartBeat.Store(fmt.Sprintf("10.0.0.%d:1001", i), time.Now().UnixNano())
	}

	getCount := func() int {
		cnt := 0
		testServer.lastHeartBeat.Range(func(k, v interface{}) bool {
			cnt++
			return true
		})

		return cnt
	}

	assert.Equal(t, maxClients, getCount())
	testServer.cleanupHeartbeats()
	assert.Equal(t, 0, getCount())
	cleanup()
}

func TestPerformTableUpdate(t *testing.T) {
	const testClients = 10
	serverAddress, testServer, cleanup := newTestPlacementServer(testRaftServer)
	testServer.hasLeadership.Store(true)

	// arrange
	clientConns := []*grpc.ClientConn{}
	clientStreams := []v1pb.Placement_ReportDaprStatusClient{}
	clientRecvDataLock := &sync.RWMutex{}
	clientRecvData := []map[string]int64{}
	clientUpToDateCh := make(chan struct{}, testClients)

	for i := 0; i < testClients; i++ {
		conn, stream, err := newTestClient(serverAddress)
		assert.NoError(t, err)
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
					clientRecvData[clientID][placementOrder.Operation] = time.Now().UnixNano()
					clientRecvDataLock.Unlock()
					// Check if the table is up to date.
					if placementOrder.Operation == "update" {
						if placementOrder.Tables != nil {
							upToDate = true
							for _, entries := range placementOrder.Tables.Entries {
								// Check if all clients are in load map.
								if len(entries.LoadMap) != testClients {
									upToDate = false
								}
							}
						}
					}
					if placementOrder.Operation == "unlock" {
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
			Name:     fmt.Sprintf("127.0.0.1:5010%d", i),
			Entities: []string{"DogActor", "CatActor"},
			Id:       "testAppID",
			Load:     1, // Not used yet
			// Port is redundant because Name should include port number
		}

		// act
		assert.NoError(t, clientStreams[i].Send(host))
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
	testServer.streamConnPoolLock.RLock()
	streamConnPool := make([]placementGRPCStream, len(testServer.streamConnPool))
	copy(streamConnPool, testServer.streamConnPool)
	testServer.streamConnPoolLock.RUnlock()
	testServer.performTablesUpdate(streamConnPool, nil)

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
