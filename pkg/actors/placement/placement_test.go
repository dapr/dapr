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
	"fmt"
	"io"
	"net"
	"strings"
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

	"github.com/dapr/dapr/pkg/actors/internal"
	"github.com/dapr/dapr/pkg/placement/hashing"
	placementv1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/kit/logger"
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
		address[i], testSrv[i], cleanup[i] = newTestServer()
	}

	var apiLevel atomic.Uint32
	apiLevel.Store(1)
	testPlacement := NewActorPlacement(internal.ActorsProviderOptions{
		Config: internal.Config{
			ActorsService:    "placement:" + strings.Join(address, ","),
			AppID:            "testAppID",
			HostAddress:      "127.0.0.1",
			Port:             1000,
			PodName:          "testPodName",
			HostedActorTypes: internal.NewHostedActors([]string{"actorOne", "actorTwo"}),
		},
		AppHealthFn: func(ctx context.Context) <-chan bool { return nil },
		Security:    testSecurity(t),
		Resiliency:  resiliency.New(logger.NewLogger("test")),
		APILevel:    &apiLevel,
	}).(*actorPlacement)

	t.Run("found leader placement in a round robin way", func(t *testing.T) {
		// set leader for leaderServer[0]
		testSrv[leaderServer[0]].setLeader(true)

		// act
		require.NoError(t, testPlacement.Start(context.Background()))
		time.Sleep(statusReportHeartbeatInterval * 3)
		assert.Equal(t, leaderServer[0], testPlacement.serverIndex.Load())
		assert.GreaterOrEqual(t, testSrv[testPlacement.serverIndex.Load()].recvCount.Load(), int32(2))
	})

	t.Run("shutdown leader and find the next leader", func(t *testing.T) {
		// shutdown server
		cleanup[leaderServer[0]]()

		time.Sleep(statusReportHeartbeatInterval)

		// set the second leader
		testSrv[leaderServer[1]].setLeader(true)

		// wait until placement connect to the second leader node
		time.Sleep(statusReportHeartbeatInterval * 3)
		assert.Equal(t, leaderServer[1], testPlacement.serverIndex.Load())
		assert.GreaterOrEqual(t, testSrv[testPlacement.serverIndex.Load()].recvCount.Load(), int32(1))
	})

	// tear down
	require.NoError(t, testPlacement.Close())
	assert.EventuallyWithT(t, func(t *assert.CollectT) {
		assert.True(t, testSrv[testPlacement.serverIndex.Load()].isGracefulShutdown.Load())
	}, statusReportHeartbeatInterval*3, 50*time.Millisecond)

	for _, fn := range cleanup {
		fn()
	}
}

func TestAppHealthyStatus(t *testing.T) {
	// arrange
	address, testSrv, cleanup := newTestServer()

	// set leader
	testSrv.setLeader(true)

	appHealthCh := make(chan bool)

	var apiLevel atomic.Uint32
	apiLevel.Store(1)
	testPlacement := NewActorPlacement(internal.ActorsProviderOptions{
		Config: internal.Config{
			ActorsService:    "placement:" + address,
			AppID:            "testAppID",
			HostAddress:      "127.0.0.1",
			Port:             1000,
			PodName:          "testPodName",
			HostedActorTypes: internal.NewHostedActors([]string{"actorOne", "actorTwo"}),
		},
		AppHealthFn: func(ctx context.Context) <-chan bool { return appHealthCh },
		Security:    testSecurity(t),
		Resiliency:  resiliency.New(logger.NewLogger("test")),
		APILevel:    &apiLevel,
	}).(*actorPlacement)

	// act
	require.NoError(t, testPlacement.Start(context.Background()))

	// wait until client sends heartbeat to the test server
	time.Sleep(statusReportHeartbeatInterval * 3)
	oldCount := testSrv.recvCount.Load()
	assert.GreaterOrEqual(t, oldCount, int32(2), "client must send at least twice")

	// Mark app unhealthy
	appHealthCh <- false
	time.Sleep(statusReportHeartbeatInterval * 2)
	assert.LessOrEqual(t, testSrv.recvCount.Load(), oldCount+1, "no more +1 heartbeat because app is unhealthy")

	// clean up
	close(appHealthCh)
	require.NoError(t, testPlacement.Close())
	cleanup()
}

func TestOnPlacementOrder(t *testing.T) {
	tableUpdateCount := atomic.Int64{}
	tableUpdateFunc := func() { tableUpdateCount.Add(1) }
	testPlacement := NewActorPlacement(internal.ActorsProviderOptions{
		Config: internal.Config{
			ActorsService:    "placement:",
			AppID:            "testAppID",
			HostAddress:      "127.0.0.1",
			Port:             1000,
			PodName:          "testPodName",
			HostedActorTypes: internal.NewHostedActors([]string{"actorOne", "actorTwo"}),
		},
		AppHealthFn: func(ctx context.Context) <-chan bool { return nil },
		Security:    testSecurity(t),
		Resiliency:  resiliency.New(logger.NewLogger("test")),
	}).(*actorPlacement)
	testPlacement.SetOnTableUpdateFn(tableUpdateFunc)

	t.Run("lock operation", func(t *testing.T) {
		testPlacement.onPlacementOrder(&placementv1pb.PlacementOrder{
			Operation: "lock",
		})

		select {
		case testPlacement.unblockSignal <- struct{}{}:
			<-testPlacement.unblockSignal
			t.Fatal("Should be blocked")
		default:
			// All good
		}
	})

	t.Run("update operation", func(t *testing.T) {
		tableVersion := "1"
		tableUpdateCount.Store(0)
		testPlacement.onPlacementOrder(&placementv1pb.PlacementOrder{
			Operation: "update",
			Tables: &placementv1pb.PlacementTables{
				Version: tableVersion,
				Entries: map[string]*placementv1pb.PlacementTable{},
			},
		})

		assert.Equal(t, int64(1), tableUpdateCount.Load())

		// no update with the same table version
		testPlacement.onPlacementOrder(&placementv1pb.PlacementOrder{
			Operation: "update",
			Tables: &placementv1pb.PlacementTables{
				Version: tableVersion,
				Entries: map[string]*placementv1pb.PlacementTable{},
			},
		})

		assert.Equal(t, int64(1), tableUpdateCount.Load())
	})
	t.Run("update operation without vnodes (after v1.13)", func(t *testing.T) {
		tableVersion := "2"

		//
		entries := map[string]*placementv1pb.PlacementTable{
			"actorOne": {
				LoadMap: map[string]*placementv1pb.Host{
					"hostname1": {
						Name: "app-1",
						Port: 3000,
						Id:   "id-1",
					},
					"hostname2": {
						Name: "app-2",
						Port: 3000,
						Id:   "id-2",
					},
				},
			},
		}

		testPlacement.apiLevel = 20
		t.Cleanup(func() {
			testPlacement.apiLevel = 10
		})

		testPlacement.onPlacementOrder(&placementv1pb.PlacementOrder{
			Operation: "update",
			Tables: &placementv1pb.PlacementTables{
				Version:           tableVersion,
				Entries:           entries,
				ApiLevel:          20,
				ReplicationFactor: 3,
			},
		})

		table := testPlacement.placementTables.Entries

		assert.Len(t, table, 1)
		assert.Containsf(t, table, "actorOne", "actorOne should be in the table")
		assert.Len(t, table["actorOne"].VirtualNodes(), 6)
		assert.Len(t, table["actorOne"].SortedSet(), 6)
	})

	t.Run("update operation with vnodes (before v1.13)", func(t *testing.T) {
		tableVersion := "3"
		tableUpdateCount.Store(0)

		//
		entries := map[string]*placementv1pb.PlacementTable{
			"actorOne": {
				LoadMap: map[string]*placementv1pb.Host{
					"hostname1": {
						Name: "app-1",
						Port: 3000,
						Id:   "id-1",
					},
					"hostname2": {
						Name: "app-2",
						Port: 3000,
						Id:   "id-2",
					},
				},
				Hosts: map[uint64]string{
					0: "hostname1",
					1: "hostname1",
					3: "hostname1",
					4: "hostname2",
					5: "hostname2",
					6: "hostname2",
				},
				SortedSet: []uint64{0, 1, 3, 4, 5, 6},
			},
		}

		testPlacement.onPlacementOrder(&placementv1pb.PlacementOrder{
			Operation: "update",
			Tables: &placementv1pb.PlacementTables{
				Version:  tableVersion,
				Entries:  entries,
				ApiLevel: 10,
			},
		})

		table := testPlacement.placementTables.Entries

		assert.Len(t, table, 1)
		assert.Containsf(t, table, "actorOne", "actorOne should be in the table")
		assert.Len(t, table["actorOne"].VirtualNodes(), 6)
		assert.Len(t, table["actorOne"].SortedSet(), 6)

		// By not sending the replication factor, we simulate an older placement service
		// In that case, we expect the vnodes and sorted set to be sent by the placement service
		testPlacement.onPlacementOrder(&placementv1pb.PlacementOrder{
			Operation: "update",
			Tables: &placementv1pb.PlacementTables{
				Version:  tableVersion,
				Entries:  entries,
				ApiLevel: 10,
			},
		})
	})

	t.Run("unlock operation", func(t *testing.T) {
		testPlacement.onPlacementOrder(&placementv1pb.PlacementOrder{
			Operation: "unlock",
		})
		select {
		case testPlacement.unblockSignal <- struct{}{}:
			<-testPlacement.unblockSignal
			// All good
		default:
			t.Fatal("Should not have been blocked")
		}
	})
}

func TestWaitUntilPlacementTableIsReady(t *testing.T) {
	var apiLevel atomic.Uint32
	apiLevel.Store(1)
	testPlacement := NewActorPlacement(internal.ActorsProviderOptions{
		Config: internal.Config{
			ActorsService:    "placement:",
			AppID:            "testAppID",
			HostAddress:      "127.0.0.1",
			Port:             1000,
			PodName:          "testPodName",
			HostedActorTypes: internal.NewHostedActors([]string{"actorOne", "actorTwo"}),
		},
		AppHealthFn: func(ctx context.Context) <-chan bool { return nil },
		Security:    testSecurity(t),
		Resiliency:  resiliency.New(logger.NewLogger("test")),
		APILevel:    &apiLevel,
	}).(*actorPlacement)

	// Set the hasPlacementTablesCh channel to nil for the first tests, indicating that the placement tables already exist
	testPlacement.hasPlacementTablesCh = nil

	t.Run("already unlocked", func(t *testing.T) {
		select {
		case testPlacement.unblockSignal <- struct{}{}:
			<-testPlacement.unblockSignal
			// All good
		default:
			t.Fatal("Should not have been blocked")
		}

		err := testPlacement.WaitUntilReady(context.Background())
		require.NoError(t, err)
	})

	t.Run("wait until ready", func(t *testing.T) {
		testPlacement.onPlacementOrder(&placementv1pb.PlacementOrder{Operation: "lock"})

		testSuccessCh := make(chan error)
		go func() {
			testSuccessCh <- testPlacement.WaitUntilReady(context.Background())
		}()

		time.Sleep(50 * time.Millisecond)
		select {
		case testPlacement.unblockSignal <- struct{}{}:
			<-testPlacement.unblockSignal
			t.Fatal("Should be blocked")
		default:
			// All good
		}

		// unlock
		testPlacement.onPlacementOrder(&placementv1pb.PlacementOrder{Operation: "unlock"})

		// ensure that it is unlocked
		select {
		case <-time.After(500 * time.Millisecond):
			t.Fatal("Placement table not unlocked in 500ms")
		case err := <-testSuccessCh:
			require.NoError(t, err)
		}

		select {
		case testPlacement.unblockSignal <- struct{}{}:
			<-testPlacement.unblockSignal
			// All good
		default:
			t.Fatal("Should not have been blocked")
		}
	})

	t.Run("abort on context canceled", func(t *testing.T) {
		testPlacement.onPlacementOrder(&placementv1pb.PlacementOrder{Operation: "lock"})

		ctx, cancel := context.WithCancel(context.Background())
		testSuccessCh := make(chan error)
		go func() {
			testSuccessCh <- testPlacement.WaitUntilReady(ctx)
		}()

		time.Sleep(50 * time.Millisecond)
		select {
		case testPlacement.unblockSignal <- struct{}{}:
			<-testPlacement.unblockSignal
			t.Fatal("Should be blocked")
		default:
			// All good
		}

		// cancel context
		cancel()

		// ensure that it is still locked
		select {
		case <-time.After(500 * time.Millisecond):
			t.Fatal("did not return in 500ms")
		case err := <-testSuccessCh:
			require.Error(t, err)
			require.ErrorIs(t, err, context.Canceled)
		}

		select {
		case testPlacement.unblockSignal <- struct{}{}:
			<-testPlacement.unblockSignal
			t.Fatal("Should be blocked")
		default:
			// All good
		}

		// Unblock for the next test
		<-testPlacement.unblockSignal
	})

	t.Run("blocks until tables have been received", func(t *testing.T) {
		hasPlacementTablesCh := make(chan struct{})
		testPlacement.hasPlacementTablesCh = hasPlacementTablesCh

		testSuccessCh := make(chan error)
		go func() {
			testSuccessCh <- testPlacement.WaitUntilReady(context.Background())
		}()

		// No signal for now
		select {
		case <-time.After(500 * time.Millisecond):
			// all good
		case <-testSuccessCh:
			t.Fatal("Received an unexpected signal")
		}

		// Close the channel
		close(hasPlacementTablesCh)

		select {
		case <-time.After(500 * time.Millisecond):
			t.Fatal("did not return in 500ms")
		case err := <-testSuccessCh:
			require.NoError(t, err)
		}
	})
}

func TestLookupActor(t *testing.T) {
	var apiLevel atomic.Uint32
	apiLevel.Store(1)
	testPlacement := NewActorPlacement(internal.ActorsProviderOptions{
		Config: internal.Config{
			ActorsService:    "placement:",
			AppID:            "testAppID",
			HostAddress:      "127.0.0.1",
			Port:             1000,
			PodName:          "testPodName",
			HostedActorTypes: internal.NewHostedActors([]string{"actorOne", "actorTwo"}),
		},
		AppHealthFn: func(ctx context.Context) <-chan bool { return nil },
		Security:    testSecurity(t),
		Resiliency:  resiliency.New(logger.NewLogger("test")),
		APILevel:    &apiLevel,
	}).(*actorPlacement)

	t.Run("Placement table is unset", func(t *testing.T) {
		_, err := testPlacement.LookupActor(context.Background(), internal.LookupActorRequest{
			ActorType: "actorOne",
			ActorID:   "test",
		})
		require.Error(t, err)
		require.ErrorContains(t, err, "did not find address for actor")
	})

	t.Run("found host and appid", func(t *testing.T) {
		testPlacement.placementTables = &hashing.ConsistentHashTables{
			Version: "1",
			Entries: map[string]*hashing.Consistent{},
		}

		actorOneHashing := hashing.NewConsistentHash(10)
		actorOneHashing.Add(testPlacement.config.GetRuntimeHostname(), testPlacement.config.AppID, 0)
		testPlacement.placementTables.Entries["actorOne"] = actorOneHashing

		// existing actor type
		lar, err := testPlacement.LookupActor(context.Background(), internal.LookupActorRequest{
			ActorType: "actorOne",
			ActorID:   "id0",
		})
		require.NoError(t, err)
		assert.Equal(t, testPlacement.config.GetRuntimeHostname(), lar.Address)
		assert.Equal(t, testPlacement.config.AppID, lar.AppID)

		// non existing actor type
		lar, err = testPlacement.LookupActor(context.Background(), internal.LookupActorRequest{
			ActorType: "nonExistingActorType",
			ActorID:   "id0",
		})
		require.Error(t, err)
		require.ErrorContains(t, err, "did not find address for actor")
		assert.Empty(t, lar.Address)
		assert.Empty(t, lar.AppID)
	})
}

func TestConcurrentUnblockPlacements(t *testing.T) {
	var apiLevel atomic.Uint32
	apiLevel.Store(1)
	testPlacement := NewActorPlacement(internal.ActorsProviderOptions{
		Config: internal.Config{
			ActorsService:    "placement:",
			AppID:            "testAppID",
			HostAddress:      "127.0.0.1",
			Port:             1000,
			PodName:          "testPodName",
			HostedActorTypes: internal.NewHostedActors([]string{"actorOne", "actorTwo"}),
		},
		AppHealthFn: func(ctx context.Context) <-chan bool { return nil },
		Security:    testSecurity(t),
		Resiliency:  resiliency.New(logger.NewLogger("test")),
		APILevel:    &apiLevel,
	}).(*actorPlacement)

	// Set the hasPlacementTablesCh channel to nil for the first tests, indicating that the placement tables already exist
	testPlacement.hasPlacementTablesCh = nil

	t.Run("concurrent unlock", func(t *testing.T) {
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

func newTestServer() (conn string, srv *testServer, cleanup func()) {
	srv = &testServer{}
	conn, cleanup = newTestServerWithOpts(func(s *grpc.Server) {
		srv.isGracefulShutdown.Store(false)
		srv.setLeader(false)
		placementv1pb.RegisterPlacementServer(s, srv)
	})
	return
}

func newTestServerWithOpts(useGrpcServer ...func(*grpc.Server)) (string, func()) {
	port, _ := freeport.GetFreePort()
	conn := fmt.Sprintf("127.0.0.1:%d", port)
	listener, _ := net.Listen("tcp", conn)
	server := grpc.NewServer()
	for _, opt := range useGrpcServer {
		opt(server)
	}

	go func() {
		server.Serve(listener)
	}()

	// wait until test server starts
	time.Sleep(100 * time.Millisecond)

	cleanup := func() {
		listener.Close()
		server.Stop()
	}

	return conn, cleanup
}

type testServer struct {
	isLeader           atomic.Bool
	recvCount          atomic.Int32
	recvError          error
	isGracefulShutdown atomic.Bool
}

func (s *testServer) ReportDaprStatus(srv placementv1pb.Placement_ReportDaprStatusServer) error { //nolint:nosnakecase
	for {
		if !s.isLeader.Load() {
			return status.Error(codes.FailedPrecondition, "only leader can serve the request")
		}

		_, err := srv.Recv()
		if errors.Is(err, io.EOF) || status.Code(err) == codes.Canceled {
			s.isGracefulShutdown.Store(true)
			return nil
		} else if err != nil {
			s.recvError = err
			return err
		}
		s.recvCount.Add(1)
	}
}

func (s *testServer) setLeader(leader bool) {
	s.isLeader.Store(leader)
}
