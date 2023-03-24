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

package internal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/phayes/freeport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/dapr/dapr/pkg/placement/hashing"
	placementv1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

func TestAddDNSResolverPrefix(t *testing.T) {
	testCases := []struct {
		addr          []string
		resolverAdded []string
	}{
		{
			addr:          []string{"placement1:50005", "placement2:50005", "placement3:50005"},
			resolverAdded: []string{"dns:///placement1:50005", "dns:///placement2:50005", "dns:///placement3:50005"},
		}, {
			addr:          []string{"192.168.0.100:50005", "192.168.0.101:50005", "192.168.0.102:50005"},
			resolverAdded: []string{"192.168.0.100:50005", "192.168.0.101:50005", "192.168.0.102:50005"},
		},
	}

	for _, tc := range testCases {
		assert.EqualValues(t, tc.resolverAdded, addDNSResolverPrefix(tc.addr))
	}
}

func TestPlacementStream_RoundRobin(t *testing.T) {
	const testServerCount = 3
	leaderServer := []int32{1, 2}

	address := make([]string, testServerCount)
	testSrv := make([]*testServer, testServerCount)
	cleanup := make([]func(), testServerCount)

	for i := 0; i < testServerCount; i++ {
		address[i], testSrv[i], cleanup[i] = newTestServer(t)
	}

	testPlacement := NewActorPlacement(Options{
		ServerAddr:         address,
		ClientCert:         nil,
		AppID:              "testAppID",
		RuntimeHostName:    "127.0.0.1:1000",
		ActorTypes:         []string{"actorOne", "actorTwo"},
		AppHealthFn:        func() bool { return true },
		AfterTableUpdateFn: func() {},
	})
	clock := clocktesting.NewFakeClock(time.Now())
	testPlacement.clock = clock

	ctx, cancel := context.WithCancel(context.Background())

	// act
	errCh := make(chan error)
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(time.Second):
			require.Fail(t, "timeout waiting for placement server to shutdown")
		}
	})

	// set leader for leaderServer[0]
	testSrv[leaderServer[0]].setLeader(true)

	go func() {
		errCh <- testPlacement.Start(ctx)
	}()

	assert.Eventually(t, clock.HasWaiters, time.Second, time.Millisecond)

	t.Run("found leader placement in a round robin way", func(t *testing.T) {
		require.Eventually(t, func() bool {
			clock.Step(statusReportHeartbeatInterval)
			return leaderServer[0] == testPlacement.serverIndex.Load() &&
				testSrv[leaderServer[0]].recvCount.Load() >= int32(2)
		}, time.Second, time.Millisecond)
	})

	t.Run("shutdown leader and find the next leader", func(t *testing.T) {
		// shutdown server
		cleanup[leaderServer[0]]()

		// set the second leader
		testSrv[leaderServer[1]].setLeader(true)

		// wait until placement connect to the second leader node
		require.Eventually(t, func() bool {
			clock.Step(statusReportHeartbeatInterval)
			return leaderServer[1] == testPlacement.serverIndex.Load() &&
				testSrv[leaderServer[1]].recvCount.Load() >= 1
		}, time.Second, time.Millisecond)
	})

	// tear down
	testPlacement.Close()
	assert.Eventually(t, func() bool {
		clock.Step(statusReportHeartbeatInterval)
		return testSrv[testPlacement.serverIndex.Load()].isGracefulShutdown.Load()
	}, time.Second, time.Millisecond)

	for _, fn := range cleanup {
		fn()
	}
}

func TestAppHealthyStatus(t *testing.T) {
	// arrange
	address, testSrv, cleanup := newTestServer(t)

	// set leader
	testSrv.setLeader(true)

	appHealth := atomic.Bool{}
	appHealth.Store(true)

	appHealthFunc := appHealth.Load
	noopTableUpdateFunc := func() {}
	testPlacement := NewActorPlacement(Options{
		ServerAddr:         []string{address},
		ClientCert:         nil,
		AppID:              "testAppID",
		RuntimeHostName:    "127.0.0.1:1000",
		ActorTypes:         []string{"actorOne", "actorTwo"},
		AppHealthFn:        appHealthFunc,
		AfterTableUpdateFn: noopTableUpdateFunc,
	})
	clock := clocktesting.NewFakeClock(time.Now())
	testPlacement.clock = clock

	// act
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// act
	errCh := make(chan error)
	t.Cleanup(func() {
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(time.Second * 5):
			require.Fail(t, "timeout waiting for placement server to shutdown")
		}
	})
	go func() {
		errCh <- testPlacement.Start(ctx)
	}()

	// wait until client sends heartbeat to the test server
	var oldCount int32
	assert.Eventually(t, func() bool {
		clock.Step(statusReportHeartbeatInterval)
		oldCount = testSrv.recvCount.Load()
		return oldCount >= 2
	}, time.Second, time.Millisecond, "client must send at least twice")

	// Mark app unhealthy
	appHealth.Store(false)
	clock.Step(statusReportHeartbeatInterval * 2)
	assert.True(t, testSrv.recvCount.Load() <= oldCount+1, "no more +1 heartbeat because app is unhealthy")

	// clean up
	testPlacement.Close()
	cleanup()
}

func TestOnPlacementOrder(t *testing.T) {
	ctx := context.Background()
	tableUpdateCount := 0
	appHealthFunc := func() bool { return true }
	tableUpdateFunc := func() { tableUpdateCount++ }
	testPlacement := NewActorPlacement(Options{
		ServerAddr:         []string{},
		ClientCert:         nil,
		AppID:              "testAppID",
		RuntimeHostName:    "127.0.0.1:1000",
		ActorTypes:         []string{"actorOne", "actorTwo"},
		AppHealthFn:        appHealthFunc,
		AfterTableUpdateFn: tableUpdateFunc,
	})

	t.Run("lock operation", func(t *testing.T) {
		testPlacement.onPlacementOrder(ctx, &placementv1pb.PlacementOrder{
			Operation: "lock",
		})
		assert.True(t, testPlacement.tableIsBlocked.Load())
	})

	t.Run("update operation", func(t *testing.T) {
		tableVersion := "1"
		tableUpdateCount = 0
		testPlacement.onPlacementOrder(ctx, &placementv1pb.PlacementOrder{
			Operation: "update",
			Tables: &placementv1pb.PlacementTables{
				Version: tableVersion,
				Entries: map[string]*placementv1pb.PlacementTable{},
			},
		})

		assert.Equal(t, 1, tableUpdateCount)

		// no update with the same table version
		testPlacement.onPlacementOrder(ctx, &placementv1pb.PlacementOrder{
			Operation: "update",
			Tables: &placementv1pb.PlacementTables{
				Version: tableVersion,
				Entries: map[string]*placementv1pb.PlacementTable{},
			},
		})

		assert.Equal(t, 1, tableUpdateCount)
	})

	t.Run("unlock operation", func(t *testing.T) {
		testPlacement.onPlacementOrder(ctx, &placementv1pb.PlacementOrder{
			Operation: "unlock",
		})
		assert.False(t, testPlacement.tableIsBlocked.Load())
	})
}

func TestWaitUntilPlacementTableIsReady(t *testing.T) {
	ctx := context.Background()
	appHealthFunc := func() bool { return true }
	tableUpdateFunc := func() {}
	testPlacement := NewActorPlacement(Options{
		ServerAddr:         []string{},
		ClientCert:         nil,
		AppID:              "testAppID",
		RuntimeHostName:    "127.0.0.1:1000",
		ActorTypes:         []string{"actorOne", "actorTwo"},
		AppHealthFn:        appHealthFunc,
		AfterTableUpdateFn: tableUpdateFunc,
	})
	clock := clocktesting.NewFakeClock(time.Now())
	testPlacement.clock = clock

	t.Run("already unlocked", func(t *testing.T) {
		require.False(t, testPlacement.tableIsBlocked.Load())

		err := testPlacement.WaitUntilPlacementTableIsReady(context.Background())
		assert.NoError(t, err)
	})

	t.Run("wait until ready", func(t *testing.T) {
		testPlacement.onPlacementOrder(ctx, &placementv1pb.PlacementOrder{Operation: "lock"})

		testSuccessCh := make(chan struct{})
		go func() {
			err := testPlacement.WaitUntilPlacementTableIsReady(context.Background())
			if assert.NoError(t, err) {
				testSuccessCh <- struct{}{}
			}
		}()

		assert.Eventually(t, func() bool {
			clock.Step(50 * time.Millisecond)
			return testPlacement.tableIsBlocked.Load()
		}, 500*time.Millisecond, time.Millisecond)

		// unlock
		testPlacement.onPlacementOrder(ctx, &placementv1pb.PlacementOrder{Operation: "unlock"})

		// ensure that it is unlocked
		select {
		case <-time.After(500 * time.Millisecond):
			t.Fatal("placement table not unlocked in 500ms")
		case <-testSuccessCh:
			// all good
		}

		assert.False(t, testPlacement.tableIsBlocked.Load())
	})

	t.Run("abort on context canceled", func(t *testing.T) {
		testPlacement.onPlacementOrder(ctx, &placementv1pb.PlacementOrder{Operation: "lock"})

		testSuccessCh := make(chan struct{})
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			err := testPlacement.WaitUntilPlacementTableIsReady(ctx)
			if assert.ErrorIs(t, err, context.Canceled) {
				testSuccessCh <- struct{}{}
			}
		}()

		assert.Eventually(t, func() bool {
			clock.Step(50 * time.Millisecond)
			return testPlacement.tableIsBlocked.Load()
		}, time.Second, time.Millisecond)

		// cancel context
		cancel()

		// ensure that it is still locked
		select {
		case <-time.After(500 * time.Millisecond):
			t.Fatal("did not return in 500ms")
		case <-testSuccessCh:
			// all good
		}

		assert.True(t, testPlacement.tableIsBlocked.Load())
	})
}

func TestLookupActor(t *testing.T) {
	appHealthFunc := func() bool { return true }
	tableUpdateFunc := func() {}
	testPlacement := NewActorPlacement(Options{
		ServerAddr:         []string{},
		ClientCert:         nil,
		AppID:              "testAppID",
		RuntimeHostName:    "127.0.0.1:1000",
		ActorTypes:         []string{"actorOne", "actorTwo"},
		AppHealthFn:        appHealthFunc,
		AfterTableUpdateFn: tableUpdateFunc,
	})

	t.Run("Placementtable is unset", func(t *testing.T) {
		name, appID := testPlacement.LookupActor("actorOne", "test")
		assert.Empty(t, name)
		assert.Empty(t, appID)
	})

	t.Run("found host and appid", func(t *testing.T) {
		const testActorType = "actorOne"
		testPlacement.placementTables = &hashing.ConsistentHashTables{
			Version: "1",
			Entries: map[string]*hashing.Consistent{},
		}

		// set vnode size
		hashing.SetReplicationFactor(10)
		actorOneHashing := hashing.NewConsistentHash()
		actorOneHashing.Add(testPlacement.runtimeHostName, testPlacement.appID, 0)
		testPlacement.placementTables.Entries[testActorType] = actorOneHashing

		// existing actor type
		name, appID := testPlacement.LookupActor(testActorType, "id0")
		assert.Equal(t, testPlacement.runtimeHostName, name)
		assert.Equal(t, testPlacement.appID, appID)

		// non existing actor type
		name, appID = testPlacement.LookupActor("nonExistingActorType", "id0")
		assert.Empty(t, name)
		assert.Empty(t, appID)
	})
}

func TestConcurrentUnblockPlacements(t *testing.T) {
	appHealthFunc := func() bool { return true }
	tableUpdateFunc := func() {}
	testPlacement := NewActorPlacement(Options{
		ServerAddr:         []string{},
		ClientCert:         nil,
		AppID:              "testAppID",
		RuntimeHostName:    "127.0.0.1:1000",
		ActorTypes:         []string{"actorOne", "actorTwo"},
		AppHealthFn:        appHealthFunc,
		AfterTableUpdateFn: tableUpdateFunc,
	})

	t.Run("concurrent_unlock", func(t *testing.T) {
		for i := 0; i < 10000; i++ {
			testPlacement.blockPlacements()
			wg := sync.WaitGroup{}
			wg.Add(2)
			go func() {
				testPlacement.unblockPlacements()
				wg.Done()
			}()
			go func() {
				testPlacement.unblockPlacements()
				wg.Done()
			}()
			// Waiting for the goroutines to finish
			wg.Wait()
		}
	})
}

func newTestServer(t *testing.T) (conn string, srv *testServer, cleanup func()) {
	t.Helper()

	srv = &testServer{}
	conn, cleanup = newTestServerWithOpts(t, func(s *grpc.Server) {
		srv.isGracefulShutdown.Store(false)
		srv.setLeader(false)
		placementv1pb.RegisterPlacementServer(s, srv)
	})
	return
}

func newTestServerWithOpts(t *testing.T, useGrpcServer ...func(*grpc.Server)) (string, func()) {
	t.Helper()

	port, _ := freeport.GetFreePort()
	conn := fmt.Sprintf("127.0.0.1:%d", port)
	listener, err := net.Listen("tcp", conn)
	require.NoError(t, err)

	server := grpc.NewServer()
	for _, opt := range useGrpcServer {
		opt(server)
	}

	serverStopped := make(chan struct{})
	go func() {
		defer close(serverStopped)
		if err := server.Serve(listener); !errors.Is(err, grpc.ErrServerStopped) {
			require.NoError(t, err)
		}
	}()

	t.Logf("test server listening on %s", conn)

	cleanup := func() {
		server.Stop()
		listener.Close()
		select {
		case <-serverStopped:
		case <-time.After(5 * time.Second):
			t.Fatal("server did not stop in 5 seconds")
		}
	}

	return conn, cleanup
}

type testServer struct {
	isLeader           atomic.Bool
	lastHost           *placementv1pb.Host
	recvCount          atomic.Int32
	lastTimestamp      time.Time
	recvError          error
	isGracefulShutdown atomic.Bool
}

func (s *testServer) ReportDaprStatus(srv placementv1pb.Placement_ReportDaprStatusServer) error { //nolint:nosnakecase
	for {
		if !s.isLeader.Load() {
			return status.Error(codes.FailedPrecondition, "only leader can serve the request")
		}

		req, err := srv.Recv()
		if errors.Is(err, io.EOF) || status.Code(err) == codes.Canceled {
			s.isGracefulShutdown.Store(true)
			return nil
		} else if err != nil {
			s.recvError = err
			return err
		}
		s.recvCount.Add(1)
		s.lastHost = req
		s.lastTimestamp = time.Now()
	}
}

func (s *testServer) setLeader(leader bool) {
	s.isLeader.Store(leader)
}
