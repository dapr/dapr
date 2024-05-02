package placement

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCleanupHeartBeats(t *testing.T) {
	_, testServer, clock, cleanup := newTestPlacementServer(t, testRaftServer)
	testServer.hasLeadership.Store(true)
	maxClients := 3

	for i := 0; i < maxClients; i++ {
		testServer.lastHeartBeat.Store(fmt.Sprintf("10.0.0.%d:1001", i), clock.Now().UnixNano())
	}

	getCount := func() int {
		cnt := 0
		testServer.lastHeartBeat.Range(func(k, v interface{}) bool {
			cnt++
			return true
		})

		return cnt
	}

	require.Equal(t, maxClients, getCount())
	testServer.cleanupHeartbeats()
	require.Equal(t, 0, getCount())
	cleanup()
}
