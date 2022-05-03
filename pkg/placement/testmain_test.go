package placement

import (
	"os"
	"testing"
	"time"

	"github.com/dapr/dapr/pkg/placement/raft"
)

var testRaftServer *raft.Server

// TestMain is executed only one time in the entire package to
// start test raft servers.
func TestMain(m *testing.M) {
	testRaftServer = raft.New("testnode", true, []raft.PeerInfo{
		{
			ID:      "testnode",
			Address: "127.0.0.1:6060",
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
