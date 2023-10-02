package placement

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	clocktesting "k8s.io/utils/clock/testing"

	"github.com/dapr/dapr/pkg/placement/raft"
	"github.com/dapr/dapr/pkg/security/fake"
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

	clock := clocktesting.NewFakeClock(time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC))

	testRaftServer = raft.New(raft.Options{
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
	})

	ctx, cancel := context.WithCancel(context.Background())
	serverStoped := make(chan struct{})
	go func() {
		defer close(serverStoped)
		if err := testRaftServer.StartRaft(ctx, fake.New(), nil); err != nil {
			log.Fatalf("error running test raft server: %v", err)
		}
	}()

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
