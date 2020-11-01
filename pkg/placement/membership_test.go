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
	"github.com/stretchr/testify/assert"
)

func cleanupStates() {
	for k := range testRaftServer.FSM().State().Members {
		testRaftServer.ApplyCommand(raft.MemberRemove, raft.DaprHostMember{
			Name: k,
		})
	}
}

func TestMembershipChangeLoop(t *testing.T) {
	testServer := NewPlacementService(testRaftServer)

	t.Run("flush timer", func(t *testing.T) {
		// arrange
		cleanupStates()
		assert.Equal(t, 0, len(testServer.raftNode.FSM().State().Members))

		testServer.shutdownCh = make(chan struct{})
		go testServer.MembershipChangeLoop()

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

		// wait until all host member change requests are flushed
		time.Sleep(flushTimerInterval + 10*time.Millisecond)

		assert.Equal(t, 3, len(testServer.raftNode.FSM().State().Members))

		close(testServer.shutdownCh)
	})

	t.Run("faulty host detector", func(t *testing.T) {
		// arrange
		cleanupStates()
		assert.Equal(t, 0, len(testServer.raftNode.FSM().State().Members))

		testServer.shutdownCh = make(chan struct{})
		go testServer.MembershipChangeLoop()

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

		// wait until all host member change requests are flushed
		time.Sleep(flushTimerInterval + 10*time.Millisecond)

		assert.Equal(t, 3, len(testServer.raftNode.FSM().State().Members))

		// faulty host detector removes all members if heartbeat does not happen
		time.Sleep(faultyHostDetectMaxDuration + 10*time.Millisecond)

		assert.Equal(t, 0, len(testServer.raftNode.FSM().State().Members))

		close(testServer.shutdownCh)
	})
}
