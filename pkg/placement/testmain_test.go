package placement

import (
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

	testRaftServer.StartRaft(nil)

	// Wait until test raft node become a leader.
	for range time.Tick(200 * time.Millisecond) {
		if testRaftServer.IsLeader() {
			break
		}
	}

	retVal := m.Run()

	testRaftServer.Shutdown()

	os.Exit(retVal)
}
