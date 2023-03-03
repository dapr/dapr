package placement

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/dapr/dapr/pkg/placement/raft"
	daprtesting "github.com/dapr/dapr/pkg/testing"
)

var testRaftServer *raft.Server

// TestMain is executed only one time in the entire package to
// start test raft servers.
func TestMain(m *testing.M) {
	ports, err := daprtesting.GetFreePorts(1)
	if err != nil {
		log.Fatalf("failed to get test server port: %v", err)
		return
	}
	testRaftServer = raft.New("testnode", true, []raft.PeerInfo{
		{
			ID:      "testnode",
			Address: fmt.Sprintf("127.0.0.1:%d", ports[0]),
		},
	}, "")

	ctx, cancel := context.WithCancel(context.Background())
	serverStoped := make(chan struct{})
	go func() {
		defer close(serverStoped)
		if err := testRaftServer.StartRaft(ctx, nil); err != nil {
			log.Fatalf("error running test raft server: %v", err)
		}
	}()

	// Wait until test raft node become a leader.
	for range time.Tick(50 * time.Millisecond) {
		if testRaftServer.IsLeader() {
			break
		}
	}

	retVal := m.Run()

	cancel()
	select {
	case <-serverStoped:
	case <-time.After(5 * time.Second):
		log.Error("server did not stop in time")
		retVal = 1
	}

	os.Exit(retVal)
}
