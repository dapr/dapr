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

func RaftOpts(t *testing.T) (*raft.Options, error) {
	t.Helper()

	ports, err := daprtesting.GetFreePorts(1)
	if err != nil {
		log.Fatalf("failed to get test server port: %v", err)
		return nil, nil
	}

	clock := clocktesting.NewFakeClock(time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC))

	return &raft.Options{
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
	}, nil
}

func RaftClusterOpts(t *testing.T) ([]*raft.Options, error) {
	t.Helper()

	ports, err := daprtesting.GetFreePorts(3)
	if err != nil {
		return nil, fmt.Errorf("failed to get test server ports %v", err)
	}

	clocks := make([]*clocktesting.FakeClock, 3)
	for i := range 3 {
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

	testRaftServersOpts := make([]*raft.Options, 0, len(raftIDs))
	for i, id := range raftIDs {
		opts := &raft.Options{
			ID:           id,
			InMem:        true,
			Peers:        peerInfo,
			LogStorePath: "",
			Clock:        clocks[i],
			Security:     fake.New(),
			Healthz:      healthz.New(),
		}
		testRaftServersOpts = append(testRaftServersOpts, opts)
	}

	return testRaftServersOpts, nil
}
