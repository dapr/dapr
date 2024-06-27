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

package placement

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/placement/tests"
)

func TestCleanupHeartBeats(t *testing.T) {
	testRaftServer := tests.Raft(t)
	_, testServer, clock, cleanup := newTestPlacementServer(t, testRaftServer)
	testServer.hasLeadership.Store(true)
	maxClients := 3

	for i := 0; i < maxClients; i++ {
		testServer.lastHeartBeat.Store(fmt.Sprintf("ns-10.0.0.%d:1001", i), clock.Now().UnixNano())
	}

	getCount := func() int {
		cnt := 0
		testServer.lastHeartBeat.Range(func(k, v any) bool {
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
