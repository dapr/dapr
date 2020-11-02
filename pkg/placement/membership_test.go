// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package placement

import (
	"fmt"
	"testing"
	"time"

	"github.com/dapr/dapr/pkg/placement/raft"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
)

func cleanupStates() {
	for k := range testRaftServer.FSM().State().Members {
		testRaftServer.ApplyCommand(raft.MemberRemove, raft.DaprHostMember{
			Name: k,
		})
	}
}

func TestMembershipChangeWorker(t *testing.T) {
	testServer := NewPlacementService(testRaftServer)

	setupEach := func(t *testing.T) {
		cleanupStates()
		assert.Equal(t, 0, len(testServer.raftNode.FSM().State().Members))

		testServer.shutdownCh = make(chan struct{})
		go testServer.MembershipChangeWorker()

		// act
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

		assert.Equal(t, 0, len(testServer.raftNode.FSM().State().Members))
	}

	tearDownEach := func() {
		close(testServer.shutdownCh)
	}

	t.Run("flush timer", func(t *testing.T) {
		// arrange
		setupEach(t)

		// wait until all host member change requests are flushed
		time.Sleep(disseminateTimerInterval + 10*time.Millisecond)
		assert.Equal(t, 3, len(testServer.raftNode.FSM().State().Members))

		tearDownEach()
	})

	t.Run("faulty host detector", func(t *testing.T) {
		// arrange
		setupEach(t)

		// wait until all host member change requests are flushed
		time.Sleep(disseminateTimerInterval + 10*time.Millisecond)
		assert.Equal(t, 3, len(testServer.raftNode.FSM().State().Members))

		// faulty host detector removes all members if heartbeat does not happen
		time.Sleep(faultyHostDetectMaxDuration + 10*time.Millisecond)
		assert.Equal(t, 0, len(testServer.raftNode.FSM().State().Members))

		tearDownEach()
	})
}

func TestPerformTableUpdate(t *testing.T) {
	const testClients = 10
	serverAddress, testServer, cleanup := newTestPlacementServer(testRaftServer)

	// arrange
	clientConns := []*grpc.ClientConn{}
	clientStreams := []v1pb.Placement_ReportDaprStatusClient{}
	clientRecvData := []map[string]int64{}

	for i := 0; i < testClients; i++ {
		conn, stream, err := newTestClient(serverAddress)
		assert.NoError(t, err)
		clientConns = append(clientConns, conn)
		clientStreams = append(clientStreams, stream)
		clientRecvData = append(clientRecvData, map[string]int64{})

		go func(clientID int) {
			for {
				placementOrder, streamErr := clientStreams[clientID].Recv()
				if streamErr != nil {
					return
				}
				if placementOrder != nil {
					clientRecvData[clientID][placementOrder.Operation] = time.Now().UnixNano()
				}
			}
		}(i)
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
	time.Sleep(10 * time.Millisecond)

	// Call performTableUpdate directly, not by MembershipChangeWorker loop.
	testServer.performTablesUpdate(testServer.streamConns, nil)

	// assert
	for i := 0; i < testClients; i++ {
		lockTime, ok := clientRecvData[i]["lock"]
		assert.True(t, ok)
		updateTime, ok := clientRecvData[i]["update"]
		assert.True(t, ok)
		unlockTime, ok := clientRecvData[i]["unlock"]
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
