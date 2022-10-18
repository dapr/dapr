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

package placement

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/phayes/freeport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/placement/raft"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

const testStreamSendLatency = 50 * time.Millisecond

func newTestPlacementServer(raftServer *raft.Server) (string, *Service, func()) {
	testServer := NewPlacementService(raftServer)

	port, _ := freeport.GetFreePort()
	go func() {
		testServer.Run(strconv.Itoa(port), nil)
	}()

	// Wait until test server starts
	time.Sleep(100 * time.Millisecond)

	cleanUpFn := func() {
		testServer.hasLeadership.Store(false)
		testServer.Shutdown()
	}

	serverAddress := "127.0.0.1:" + strconv.Itoa(port)
	return serverAddress, testServer, cleanUpFn
}

func newTestClient(serverAddress string) (*grpc.ClientConn, v1pb.Placement_ReportDaprStatusClient, error) { //nolint:nosnakecase
	conn, err := grpc.Dial(serverAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	client := v1pb.NewPlacementClient(conn)
	stream, err := client.ReportDaprStatus(context.Background())
	if err != nil {
		conn.Close()
		return nil, nil, err
	}

	return conn, stream, nil
}

func TestMemberRegistration_NoLeadership(t *testing.T) {
	// set up
	serverAddress, testServer, cleanup := newTestPlacementServer(testRaftServer)
	testServer.hasLeadership.Store(false)

	// arrange
	conn, stream, err := newTestClient(serverAddress)
	assert.NoError(t, err)

	host := &v1pb.Host{
		Name:     "127.0.0.1:50102",
		Entities: []string{"DogActor", "CatActor"},
		Id:       "testAppID",
		Load:     1, // Not used yet
		// Port is redundant because Name should include port number
	}

	// act
	stream.Send(host)
	_, err = stream.Recv()
	s, ok := status.FromError(err)

	// assert
	assert.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, s.Code())
	stream.CloseSend()

	// tear down
	conn.Close()
	cleanup()
}

func TestMemberRegistration_Leadership(t *testing.T) {
	serverAddress, testServer, cleanup := newTestPlacementServer(testRaftServer)
	testServer.hasLeadership.Store(true)

	t.Run("Connect server and disconnect it gracefully", func(t *testing.T) {
		// arrange
		conn, stream, err := newTestClient(serverAddress)
		assert.NoError(t, err)

		host := &v1pb.Host{
			Name:     "127.0.0.1:50102",
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
			assert.Equal(t, 1, len(testServer.streamConnPool))

		case <-time.After(testStreamSendLatency):
			assert.True(t, false, "no membership change")
		}

		// act
		// Runtime needs to close stream gracefully which will let placement remove runtime host from hashing ring
		// in the next flush time window.
		stream.CloseSend()

		// assert
		select {
		case memberChange := <-testServer.membershipCh:
			assert.Equal(t, raft.MemberRemove, memberChange.cmdType)
			assert.Equal(t, host.Name, memberChange.host.Name)

		case <-time.After(testStreamSendLatency):
			require.True(t, false, "no membership change")
		}

		conn.Close()
	})

	t.Run("Connect server and disconnect it forcefully", func(t *testing.T) {
		// arrange
		conn, stream, err := newTestClient(serverAddress)
		assert.NoError(t, err)

		// act
		host := &v1pb.Host{
			Name:     "127.0.0.1:50103",
			Entities: []string{"DogActor", "CatActor"},
			Id:       "testAppID",
			Load:     1, // Not used yet
			// Port is redundant because Name should include port number
		}
		stream.Send(host)

		// assert
		select {
		case memberChange := <-testServer.membershipCh:
			assert.Equal(t, raft.MemberUpsert, memberChange.cmdType)
			assert.Equal(t, host.Name, memberChange.host.Name)
			assert.Equal(t, host.Id, memberChange.host.AppID)
			assert.EqualValues(t, host.Entities, memberChange.host.Entities)
			testServer.streamConnPoolLock.Lock()
			l := len(testServer.streamConnPool)
			testServer.streamConnPoolLock.Unlock()
			assert.Equal(t, 1, l)

		case <-time.After(testStreamSendLatency):
			require.True(t, false, "no membership change")
		}

		// act
		// Close tcp connection before closing stream, which simulates the scenario
		// where dapr runtime disconnects the connection from placement service unexpectedly.
		conn.Close()

		// assert
		select {
		case <-testServer.membershipCh:
			require.True(t, false, "should not have any member change message because faulty host detector time will clean up")

		case <-time.After(testStreamSendLatency):
			testServer.streamConnPoolLock.RLock()
			streamConnCount := len(testServer.streamConnPool)
			testServer.streamConnPoolLock.RUnlock()
			assert.Equal(t, 0, streamConnCount)
		}
	})

	t.Run("non actor host", func(t *testing.T) {
		// arrange
		conn, stream, err := newTestClient(serverAddress)
		assert.NoError(t, err)

		// act
		host := &v1pb.Host{
			Name:     "127.0.0.1:50104",
			Entities: []string{},
			Id:       "testAppID",
			Load:     1, // Not used yet
			// Port is redundant because Name should include port number
		}
		stream.Send(host)

		// assert
		select {
		case <-testServer.membershipCh:
			require.True(t, false, "should not have any membership change")

		case <-time.After(testStreamSendLatency):
			require.True(t, true)
		}

		// act
		// Close tcp connection before closing stream, which simulates the scenario
		// where dapr runtime disconnects the connection from placement service unexpectedly.
		conn.Close()
	})

	cleanup()
}
