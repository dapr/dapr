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
	"sync/atomic"
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

	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/placement/raft"
	"github.com/dapr/dapr/pkg/placement/tests"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	securityfake "github.com/dapr/dapr/pkg/security/fake"
)

func newTestPlacementServer(t *testing.T, raftOptions raft.Options) (string, *Service, *clocktesting.FakeClock, context.CancelFunc) {
	t.Helper()

	port, err := freeport.GetFreePort()
	require.NoError(t, err)

	testServer, err := New(ServiceOpts{
		Raft:               raftOptions,
		SecProvider:        securityfake.New(),
		Port:               port,
		Healthz:            healthz.New(),
		DisseminateTimeout: 2 * time.Second,
	})
	require.NoError(t, err)
	clock := clocktesting.NewFakeClock(time.Now())
	testServer.clock = clock

	serverStopped := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		defer close(serverStopped)
		err := testServer.Run(ctx)
		if !errors.Is(err, grpc.ErrServerStopped) {
			assert.NoError(t, err)
		}
	}()

	require.Eventually(t, func() bool {
		conn, err := net.Dial("tcp", ":"+strconv.Itoa(port))
		if err == nil {
			conn.Close()
		}
		return err == nil
	}, time.Second*20, time.Millisecond, "server did not start in time")

	cleanUpFn := func() {
		cancel()
		select {
		case <-serverStopped:
		case <-time.After(time.Second * 20):
			t.Error("server did not stop in time")
		}
	}

	serverAddress := "127.0.0.1:" + strconv.Itoa(port)
	return serverAddress, testServer, clock, cleanUpFn
}

func newTestClient(t *testing.T, serverAddress string) (*grpc.ClientConn, *net.TCPConn, v1pb.Placement_ReportDaprStatusClient) { //nolint:nosnakecase
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	tcpConn, err := net.Dial("tcp", serverAddress)
	require.NoError(t, err)
	conn, err := grpc.DialContext(ctx, "", //nolint:staticcheck
		grpc.WithContextDialer(func(ctx context.Context, s string) (net.Conn, error) {
			return tcpConn, nil
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(), //nolint:staticcheck
	)
	require.NoError(t, err)

	client := v1pb.NewPlacementClient(conn)

	stream, err := client.ReportDaprStatus(context.Background())
	require.NoError(t, err)

	return conn, tcpConn.(*net.TCPConn), stream
}

func TestMemberRegistration_NoLeadership(t *testing.T) {
	raftOpts, err := tests.RaftOpts(t)
	require.NoError(t, err)

	serverAddress, testServer, _, cleanup := newTestPlacementServer(t, *raftOpts)
	t.Cleanup(cleanup)
	testServer.hasLeadership.Store(false)

	conn, _, stream := newTestClient(t, serverAddress)

	host := &v1pb.Host{
		Name:      "127.0.0.1:50102",
		Namespace: "ns1",
		Entities:  []string{"DogActor", "CatActor"},
		Id:        "testAppID",
		Load:      1, // Not used yet
		// Port is redundant because Name should include port number
	}

	stream.Send(host)
	_, err = stream.Recv()
	s, ok := status.FromError(err)

	require.True(t, ok)
	require.Equal(t, codes.FailedPrecondition, s.Code())
	stream.CloseSend()

	conn.Close()
}

func TestRequiresUpdateInPlacementTables(t *testing.T) {
	hostWithActors := &v1pb.Host{
		Name:      "127.0.0.1:50100",
		Namespace: "ns1",
		Entities:  []string{"actor1", "actor2"},
		Id:        "testAppID1",
		Load:      1,
	}

	hostWithNoActors := &v1pb.Host{
		Name:      "127.0.0.1:50100",
		Namespace: "ns1",
		Entities:  []string{},
		Id:        "testAppID1",
		Load:      1,
	}

	tests := []struct {
		name        string
		isActorHost bool
		host        *v1pb.Host
		expected    bool
	}{
		{
			name:        "host with actors - updating actor types",
			isActorHost: true,
			host:        hostWithActors,
			expected:    true,
		},
		{
			name:        "host with no actors - registering new actors",
			isActorHost: false,
			host:        hostWithActors,
			expected:    true,
		},
		{
			name:        "host with actors - removing all actors",
			isActorHost: true,
			host:        hostWithNoActors,
			expected:    true,
		},
		{
			name:        "host with no actors - not registering any new actors",
			isActorHost: false,
			host:        hostWithNoActors,
			expected:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var isActorHost atomic.Bool
			isActorHost.Store(tt.isActorHost)
			assert.Equal(t, tt.expected, requiresUpdateInPlacementTables(tt.host, &isActorHost))
		})
	}
}
