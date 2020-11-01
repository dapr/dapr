// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package placement

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/dapr/dapr/pkg/placement/raft"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

var testRaftServer *raft.Server

func TestMain(m *testing.M) {
	testRaftServer = raft.New("testnode", true, true, []raft.PeerInfo{
		{
			ID:      "testnode",
			Address: "127.0.0.1:6060",
		},
	})

	testRaftServer.StartRaft(nil)

	for {
		if testRaftServer.IsLeader() {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	retVal := m.Run()

	testRaftServer.Shutdown()

	os.Exit(retVal)
}

func TestMemberRegistration(t *testing.T) {
	testServer := NewPlacementService(testRaftServer)

	go func() {
		testServer.Run("60007", nil)
	}()

	t.Run("Connect server and disconnect it gracefully", func(t *testing.T) {
		// arrange
		conn, err := grpc.Dial("127.0.0.1:60007", grpc.WithInsecure())
		assert.NoError(t, err)
		client := v1pb.NewPlacementClient(conn)
		stream, err := client.ReportDaprStatus(context.Background())
		assert.NoError(t, err)

		host := &v1pb.Host{
			Name:     "127.0.0.1:60007",
			Entities: []string{"DogActor", "CatActor"},
			Id:       "testAppID",
			Load:     1, // Not used yet
			// Port is redundant because Name should include port number
		}

		// act
		stream.Send(host)

		// assert
		select {
		case memberChange := <-testServer.membershipCh:
			assert.Equal(t, raft.MemberUpsert, memberChange.cmdType)
			assert.Equal(t, host.Name, memberChange.host.Name)
			assert.Equal(t, host.Id, memberChange.host.AppID)
			assert.EqualValues(t, host.Entities, memberChange.host.Entities)
			assert.Equal(t, 1, len(testServer.streamConns))

		case <-time.After(100 * time.Millisecond):
			require.True(t, false, "no membership change")
		}

		// act
		stream.CloseSend()

		// assert
		select {
		case memberChange := <-testServer.membershipCh:
			assert.Equal(t, raft.MemberRemove, memberChange.cmdType)
			assert.Equal(t, host.Name, memberChange.host.Name)

		case <-time.After(100 * time.Millisecond):
			require.True(t, false, "no membership change")
			assert.Equal(t, 0, len(testServer.streamConns))
		}

		conn.Close()
	})

	t.Run("Connect server and disconnect it forcefully", func(t *testing.T) {
		// arrange
		conn, err := grpc.Dial("127.0.0.1:60007", grpc.WithInsecure())
		assert.NoError(t, err)
		client := v1pb.NewPlacementClient(conn)
		stream, err := client.ReportDaprStatus(context.Background())
		assert.NoError(t, err)

		host := &v1pb.Host{
			Name:     "127.0.0.1:60007",
			Entities: []string{"DogActor", "CatActor"},
			Id:       "testAppID",
			Load:     1, // Not used yet
			// Port is redundant because Name should include port number
		}

		// act
		stream.Send(host)

		// assert
		select {
		case memberChange := <-testServer.membershipCh:
			assert.Equal(t, raft.MemberUpsert, memberChange.cmdType)
			assert.Equal(t, host.Name, memberChange.host.Name)
			assert.Equal(t, host.Id, memberChange.host.AppID)
			assert.EqualValues(t, host.Entities, memberChange.host.Entities)
			assert.Equal(t, 1, len(testServer.streamConns))

		case <-time.After(100 * time.Millisecond):
			require.True(t, false, "no membership change")
		}

		// act
		conn.Close()

		// assert
		select {
		case <-testServer.membershipCh:
			require.True(t, false, "should not have any member change message")

		case <-time.After(100 * time.Millisecond):
			assert.Equal(t, 0, len(testServer.streamConns))
			break
		}
	})

	testServer.Shutdown()
}
