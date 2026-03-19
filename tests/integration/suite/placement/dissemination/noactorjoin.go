/*
Copyright 2026 The Dapr Authors
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

package dissemination

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(noactorjoin))
}

// noactorjoin verifies that a sidecar with no actors connecting during an
// active dissemination round is properly handled. When the waiting connections
// are batched and none of them have actors, no new dissemination round should
// be started. Instead, the no-actor sidecars should receive a one-shot table
// push so they can route actor invocations.
type noactorjoin struct {
	place *placement.Placement
}

func (n *noactorjoin) Setup(t *testing.T) []framework.Option {
	n.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second*5),
	)

	return []framework.Option{
		framework.WithProcesses(n.place),
	}
}

func (n *noactorjoin) Run(t *testing.T, ctx context.Context) {
	n.place.WaitUntilRunning(t, ctx)

	assert.Eventually(t, func() bool {
		return n.place.IsLeader(t, ctx)
	}, time.Second*10, time.Millisecond*10)

	client := n.place.Client(t, ctx)

	// Connect stream1 with actors to start a dissemination round.
	stream1, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, stream1.Send(&v1pb.Host{
		Name:      "app-with-actors",
		Port:      1234,
		Entities:  []string{"actorA"},
		Id:        "app-with-actors",
		Namespace: "default",
	}))

	resp, err := stream1.Recv()
	require.NoError(t, err)
	require.Equal(t, "lock", resp.GetOperation())

	// While the round is in progress, connect a no-actor sidecar.
	// It will be queued in waitingToDisseminate since we're mid-round.
	stream2, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, stream2.Send(&v1pb.Host{
		Name:      "app-no-actors",
		Port:      5678,
		Id:        "app-no-actors",
		Namespace: "default",
	}))

	// Complete the dissemination round for stream1: LOCK ack → UPDATE → UPDATE ack → UNLOCK.
	require.NoError(t, stream1.Send(&v1pb.Host{
		Name:      "app-with-actors",
		Port:      1234,
		Entities:  []string{"actorA"},
		Id:        "app-with-actors",
		Namespace: "default",
	}))
	resp, err = stream1.Recv()
	require.NoError(t, err)
	require.Equal(t, "update", resp.GetOperation())

	require.NoError(t, stream1.Send(&v1pb.Host{
		Name:      "app-with-actors",
		Port:      1234,
		Entities:  []string{"actorA"},
		Id:        "app-with-actors",
		Namespace: "default",
	}))
	resp, err = stream1.Recv()
	require.NoError(t, err)
	require.Equal(t, "unlock", resp.GetOperation())

	// Acknowledge the UNLOCK so the server completes round 1 and processes
	// waiting connections.
	require.NoError(t, stream1.Send(&v1pb.Host{
		Name:      "app-with-actors",
		Port:      1234,
		Entities:  []string{"actorA"},
		Id:        "app-with-actors",
		Namespace: "default",
	}))

	// After round 1 completes, the server processes the waiting no-actor
	// connection. Since it has no actors (no store change), the server sends
	// a one-shot table push (LOCK+UPDATE+UNLOCK) to stream2 only.
	resp, err = stream2.Recv()
	require.NoError(t, err)
	require.Equal(t, "lock", resp.GetOperation())

	resp, err = stream2.Recv()
	require.NoError(t, err)
	require.Equal(t, "update", resp.GetOperation())

	resp, err = stream2.Recv()
	require.NoError(t, err)
	require.Equal(t, "unlock", resp.GetOperation())

	// Verify placement tables show only the host with actors.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := n.place.PlacementTables(t, ctx)
		if !assert.NotNil(c, table.Tables["default"]) {
			return
		}
		assert.Len(c, table.Tables["default"].Hosts, 1)
		assert.Equal(c, "app-with-actors", table.Tables["default"].Hosts[0].ID)
	}, time.Second*10, time.Millisecond*10)

	// No further dissemination round should occur after no-actor
	// connections were drained. Verify no unexpected messages.
	errCh := make(chan error, 1)
	go func() {
		_, serr := stream1.Recv()
		errCh <- serr
	}()

	select {
	case <-time.After(2 * time.Second):
		// Good - no unexpected dissemination round started.
	case serr := <-errCh:
		require.Fail(t, "unexpected message or error on stream1", "error: %v", serr)
	}
}
