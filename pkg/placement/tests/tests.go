/*
Copyright 2024 The Dapr Authors
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

package tests

import (
	"context"
	"fmt"
	"log"
	"testing"
	"time"

	clocktesting "k8s.io/utils/clock/testing"

	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/placement/raft"
	"github.com/dapr/dapr/pkg/security/fake"
	daprtesting "github.com/dapr/dapr/pkg/testing"
)

func Raft(t *testing.T) *raft.Server {
	t.Helper()

	ports, err := daprtesting.GetFreePorts(1)
	if err != nil {
		log.Fatalf("failed to get test server port: %v", err)
		return nil
	}

	clock := clocktesting.NewFakeClock(time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC))

	testRaftServer := raft.New(raft.Options{
		ID:    "testnode",
		InMem: true,
		Peers: []raft.PeerInfo{
			{
				ID:      "testnode",
				Address: fmt.Sprintf("127.0.0.1:%d", ports[0]),
			},
		},
		LogStorePath: "",
		Clock:        clock,
		Security:     fake.New(),
		Healthz:      healthz.New(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	serverStopped := make(chan struct{})
	go func() {
		defer close(serverStopped)
		if err := testRaftServer.StartRaft(ctx); err != nil {
			log.Fatalf("error running test raft server: %v", err)
		}
	}()
	t.Cleanup(cancel)

	// Wait until test raft node become a leader.
	for range time.Tick(time.Microsecond) {
		clock.Step(time.Second * 2)
		if testRaftServer.IsLeader() {
			break
		}
	}

	// It is painful that we have to include a `time.Sleep` here, but due to the
	// non-deterministic behaviour of the raft library we are using we will fail
	// later fail on slower test runner machines. A clock timer wait means we
	// have a _better_ chance of being in the right spot in the state machine and
	// the network has died down. Ideally we should move to a different raft
	// library that is more deterministic and reliable for our use case.
	time.Sleep(time.Second * 3)

	return testRaftServer
}

func RaftCluster(t *testing.T, parentCtx context.Context) ([]*raft.Server, error) {
	t.Helper()

	ports, err := daprtesting.GetFreePorts(3)
	if err != nil {
		return nil, fmt.Errorf("failed to get test server ports %v", err)
	}

	clocks := make([]*clocktesting.FakeClock, 3)
	for i := 0; i < 3; i++ {
		clocks[i] = clocktesting.NewFakeClock(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	}

	raftIDs := []string{"testnode1", "testnode2", "testnode3"}
	peerInfo := make([]raft.PeerInfo, 0, len(raftIDs))
	for i, id := range raftIDs {
		peerInfo = append(peerInfo, raft.PeerInfo{
			ID:      id,
			Address: fmt.Sprintf("127.0.0.1:%d", ports[i]),
		})
	}

	testRaftServers := make([]*raft.Server, 0, len(raftIDs))
	for i, id := range raftIDs {
		testRaftServer := raft.New(raft.Options{
			ID:           id,
			InMem:        true,
			Peers:        peerInfo,
			LogStorePath: "",
			Clock:        clocks[i],
			Security:     fake.New(),
			Healthz:      healthz.New(),
		})
		testRaftServers = append(testRaftServers, testRaftServer)
	}

	ctx, cancel := context.WithCancel(parentCtx)

	for _, testRaftServer := range testRaftServers {
		go func(server *raft.Server) {
			if err := server.StartRaft(ctx); err != nil {
				log.Fatalf("error running test raft server: %v", err)
			}
		}(testRaftServer)
	}
	t.Cleanup(cancel)

	return testRaftServers, nil
}
