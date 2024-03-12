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
	"errors"
	"net"
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
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/dapr/dapr/pkg/placement/raft"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	securityfake "github.com/dapr/dapr/pkg/security/fake"
)

const testStreamSendLatency = time.Second

func newTestPlacementServer(t *testing.T, raftServer *raft.Server) (string, *Service, *clocktesting.FakeClock, context.CancelFunc) {
	t.Helper()

	testServer := NewPlacementService(PlacementServiceOpts{
		RaftNode:    raftServer,
		SecProvider: securityfake.New(),
	})
	clock := clocktesting.NewFakeClock(time.Now())
	testServer.clock = clock

	port, err := freeport.GetFreePort()
	require.NoError(t, err)

	serverStopped := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		defer close(serverStopped)
		err := testServer.Run(ctx, strconv.Itoa(port))
		if !errors.Is(err, grpc.ErrServerStopped) {
			require.NoError(t, err)
		}
	}()

	assert.Eventually(t, func() bool {
		conn, err := net.Dial("tcp", ":"+strconv.Itoa(port))
		if err == nil {
			conn.Close()
		}
		return err == nil
	}, time.Second*5, time.Millisecond, "server did not start in time")

	cleanUpFn := func() {
		cancel()
		select {
		case <-serverStopped:
		case <-time.After(time.Second * 5):
			t.Error("server did not stop in time")
		}
	}

	serverAddress := "127.0.0.1:" + strconv.Itoa(port)
	return serverAddress, testServer, clock, cleanUpFn
}

func newTestClient(t *testing.T, serverAddress string) (*grpc.ClientConn, *net.TCPConn, v1pb.Placement_ReportDaprStatusClient) { //nolint:nosnakecase
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	tcpConn, err := net.Dial("tcp", serverAddress)
	require.NoError(t, err)
	conn, err := grpc.DialContext(ctx, "",
		grpc.WithContextDialer(func(ctx context.Context, s string) (net.Conn, error) {
			return tcpConn, nil
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	require.NoError(t, err)

	client := v1pb.NewPlacementClient(conn)
	stream, err := client.ReportDaprStatus(context.Background())
	require.NoError(t, err)

	return conn, tcpConn.(*net.TCPConn), stream
}

func TestMemberRegistration_NoLeadership(t *testing.T) {
	// set up
	serverAddress, testServer, _, cleanup := newTestPlacementServer(t, testRaftServer)
	t.Cleanup(cleanup)
	testServer.hasLeadership.Store(false)

	// arrange
	conn, _, stream := newTestClient(t, serverAddress)

	host := &v1pb.Host{
		Name:     "127.0.0.1:50102",
		Entities: []string{"DogActor", "CatActor"},
		Id:       "testAppID",
		Load:     1, // Not used yet
		// Port is redundant because Name should include port number
	}

	// act
	stream.Send(host)
	_, err := stream.Recv()
	s, ok := status.FromError(err)

	// assert
	assert.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, s.Code())
	stream.CloseSend()

	// tear down
	conn.Close()
}

func TestMemberRegistration_Leadership(t *testing.T) {
	serverAddress, testServer, clock, cleanup := newTestPlacementServer(t, testRaftServer)
	t.Cleanup(cleanup)
	testServer.hasLeadership.Store(true)

	t.Run("Connect server and disconnect it gracefully", func(t *testing.T) {
		// arrange
		conn, _, stream := newTestClient(t, serverAddress)

		host := &v1pb.Host{
			Name:     "127.0.0.1:50102",
			Entities: []string{"DogActor", "CatActor"},
			Id:       "testAppID",
			Load:     1, // Not used yet
			// Port is redundant because Name should include port number
		}

		// act
		require.NoError(t, stream.Send(host))

		// assert
		assert.Eventually(t, func() bool {
			clock.Step(disseminateTimerInterval)
			select {
			case memberChange := <-testServer.membershipCh:
				assert.Equal(t, raft.MemberUpsert, memberChange.cmdType)
				assert.Equal(t, host.GetName(), memberChange.host.Name)
				assert.Equal(t, host.GetId(), memberChange.host.AppID)
				assert.EqualValues(t, host.GetEntities(), memberChange.host.Entities)
				assert.Len(t, testServer.streamConnPool, 1)
				return true
			default:
				return false
			}
		}, testStreamSendLatency+3*time.Second, time.Millisecond, "no membership change")

		// act
		// Runtime needs to close stream gracefully which will let placement remove runtime host from hashing ring
		// in the next flush time window.
		stream.CloseSend()

		// assert
		select {
		case memberChange := <-testServer.membershipCh:
			assert.Equal(t, raft.MemberRemove, memberChange.cmdType)
			assert.Equal(t, host.GetName(), memberChange.host.Name)

		case <-time.After(testStreamSendLatency):
			require.Fail(t, "no membership change")
		}

		conn.Close()
	})

	t.Run("Connect server and disconnect it forcefully", func(t *testing.T) {
		// arrange
		_, tcpConn, stream := newTestClient(t, serverAddress)

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
		assert.EventuallyWithT(t, func(t *assert.CollectT) {
			clock.Step(disseminateTimerInterval)
			select {
			case memberChange := <-testServer.membershipCh:
				assert.Equal(t, raft.MemberUpsert, memberChange.cmdType)
				assert.Equal(t, host.GetName(), memberChange.host.Name)
				assert.Equal(t, host.GetId(), memberChange.host.AppID)
				assert.EqualValues(t, host.GetEntities(), memberChange.host.Entities)
				testServer.streamConnPoolLock.Lock()
				l := len(testServer.streamConnPool)
				testServer.streamConnPoolLock.Unlock()
				assert.Equal(t, 1, l)
			default:
				assert.Fail(t, "No member change")
			}
		}, testStreamSendLatency+3*time.Second, time.Millisecond, "no membership change")

		// act
		// Close tcp connection before closing stream, which simulates the scenario
		// where dapr runtime disconnects the connection from placement service unexpectedly.
		// Use SetLinger to forcefully close the TCP connection.
		tcpConn.SetLinger(0)
		tcpConn.Close()

		// assert
		select {
		case <-testServer.membershipCh:
			require.Fail(t, "should not have any member change message because faulty host detector time will clean up")

		case <-time.After(testStreamSendLatency):
			testServer.streamConnPoolLock.RLock()
			streamConnCount := len(testServer.streamConnPool)
			testServer.streamConnPoolLock.RUnlock()
			assert.Equal(t, 0, streamConnCount)
		}
	})

	t.Run("non actor host", func(t *testing.T) {
		// arrange
		conn, _, stream := newTestClient(t, serverAddress)

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
			require.Fail(t, "should not have any membership change")

		case <-time.After(testStreamSendLatency):
			// All good
		}

		// act
		// Close tcp connection before closing stream, which simulates the scenario
		// where dapr runtime disconnects the connection from placement service unexpectedly.
		require.NoError(t, conn.Close())
	})
}
