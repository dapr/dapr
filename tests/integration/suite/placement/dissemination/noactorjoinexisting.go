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
	suite.Register(new(noactorjoinexisting))
}

// noactorjoinexisting verifies that when a new no-actor sidecar connects
// during a dissemination round, the one-shot table push is sent only to the
// newly added stream and NOT to existing no-actor streams that already
// received a table push in a previous round. This prevents redundant traffic
// during scale events.
type noactorjoinexisting struct {
	place *placement.Placement
}

func (n *noactorjoinexisting) Setup(t *testing.T) []framework.Option {
	n.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second*5),
	)

	return []framework.Option{
		framework.WithProcesses(n.place),
	}
}

func (n *noactorjoinexisting) Run(t *testing.T, ctx context.Context) {
	n.place.WaitUntilRunning(t, ctx)

	assert.Eventually(t, func() bool {
		return n.place.IsLeader(t, ctx)
	}, time.Second*10, time.Millisecond*10)

	client := n.place.Client(t, ctx)

	stream1, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, stream1.Send(&v1pb.Host{
		Name:      "app-actors-1",
		Port:      1234,
		Entities:  []string{"actorA"},
		Id:        "app-actors-1",
		Namespace: "default",
	}))

	resp, err := stream1.Recv()
	require.NoError(t, err)
	require.Equal(t, "lock", resp.GetOperation())

	// While round 1 is in progress, connect a no-actor sidecar (stream2).
	stream2, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, stream2.Send(&v1pb.Host{
		Name:      "app-no-actors-1",
		Port:      5678,
		Id:        "app-no-actors-1",
		Namespace: "default",
	}))

	// Complete round 1: LOCK ack → UPDATE → UPDATE ack → UNLOCK → UNLOCK ack.
	n.completeDisseminationRound(t, stream1, &v1pb.Host{
		Name:      "app-actors-1",
		Port:      1234,
		Entities:  []string{"actorA"},
		Id:        "app-actors-1",
		Namespace: "default",
	})

	// stream2 should receive the one-shot table push (LOCK+UPDATE+UNLOCK).
	for _, expectedOp := range []string{"lock", "update", "unlock"} {
		resp, err = stream2.Recv()
		require.NoError(t, err)
		require.Equal(t, expectedOp, resp.GetOperation(),
			"stream2 one-shot push: expected %s", expectedOp)
	}

	// --- Round 2: stream3 (with actors) connects, stream4 (no actors) joins ---
	// stream2 is now in the server's streams map and participates in
	// namespace-wide dissemination alongside stream1 and stream3.

	host1 := &v1pb.Host{
		Name: "app-actors-1", Port: 1234, Entities: []string{"actorA"},
		Id: "app-actors-1", Namespace: "default",
	}
	host2 := &v1pb.Host{
		Name: "app-no-actors-1", Port: 5678,
		Id: "app-no-actors-1", Namespace: "default",
	}
	host3 := &v1pb.Host{
		Name: "app-actors-2", Port: 2345, Entities: []string{"actorB"},
		Id: "app-actors-2", Namespace: "default",
	}

	stream3, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, stream3.Send(host3))

	// All three existing streams receive LOCK for round 2.
	resp, err = stream1.Recv()
	require.NoError(t, err)
	require.Equal(t, "lock", resp.GetOperation())

	resp, err = stream2.Recv()
	require.NoError(t, err)
	require.Equal(t, "lock", resp.GetOperation())

	resp, err = stream3.Recv()
	require.NoError(t, err)
	require.Equal(t, "lock", resp.GetOperation())

	// While round 2 is in progress, connect another no-actor sidecar (stream4).
	stream4, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, stream4.Send(&v1pb.Host{
		Name: "app-no-actors-2", Port: 6789,
		Id: "app-no-actors-2", Namespace: "default",
	}))

	// Complete round 2 for all three streams.
	// All ack LOCK.
	for _, s := range []struct {
		stream v1pb.Placement_ReportDaprStatusClient
		host   *v1pb.Host
	}{
		{stream1, host1},
		{stream2, host2},
		{stream3, host3},
	} {
		require.NoError(t, s.stream.Send(s.host))
	}

	// All should get UPDATE.
	for _, s := range []v1pb.Placement_ReportDaprStatusClient{stream1, stream2, stream3} {
		resp, err = s.Recv()
		require.NoError(t, err)
		require.Equal(t, "update", resp.GetOperation())
	}

	// All ack UPDATE.
	for _, s := range []struct {
		stream v1pb.Placement_ReportDaprStatusClient
		host   *v1pb.Host
	}{
		{stream1, host1},
		{stream2, host2},
		{stream3, host3},
	} {
		require.NoError(t, s.stream.Send(s.host))
	}

	// All should get UNLOCK.
	for _, s := range []v1pb.Placement_ReportDaprStatusClient{stream1, stream2, stream3} {
		resp, err = s.Recv()
		require.NoError(t, err)
		require.Equal(t, "unlock", resp.GetOperation())
	}

	// All ack UNLOCK.
	for _, s := range []struct {
		stream v1pb.Placement_ReportDaprStatusClient
		host   *v1pb.Host
	}{
		{stream1, host1},
		{stream2, host2},
		{stream3, host3},
	} {
		require.NoError(t, s.stream.Send(s.host))
	}

	// stream4 (newly added no-actor) should receive the one-shot push.
	for _, expectedOp := range []string{"lock", "update", "unlock"} {
		resp, err = stream4.Recv()
		require.NoError(t, err)
		require.Equal(t, expectedOp, resp.GetOperation(),
			"stream4 one-shot push: expected %s", expectedOp)
	}

	// stream2 (existing no-actor from round 1) should NOT receive any
	// additional messages — the one-shot push must target only new streams.
	stream2ErrCh := make(chan error, 1)
	go func() {
		_, serr := stream2.Recv()
		stream2ErrCh <- serr
	}()

	select {
	case <-time.After(2 * time.Second):
		// Good — stream2 did not receive a redundant table push.
	case serr := <-stream2ErrCh:
		require.Fail(t, "stream2 received unexpected message or error after round 2",
			"error: %v", serr)
	}
}

// completeDisseminationRound drives a single-stream dissemination round from
// the current LOCK phase through UNLOCK acknowledgment.
func (n *noactorjoinexisting) completeDisseminationRound(t *testing.T, stream v1pb.Placement_ReportDaprStatusClient, host *v1pb.Host) {
	t.Helper()

	// Ack LOCK → receive UPDATE.
	require.NoError(t, stream.Send(host))
	resp, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "update", resp.GetOperation())

	// Ack UPDATE → receive UNLOCK.
	require.NoError(t, stream.Send(host))
	resp, err = stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "unlock", resp.GetOperation())

	// Ack UNLOCK.
	require.NoError(t, stream.Send(host))
}
