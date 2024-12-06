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

package dissemination

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(streamHang))
}

type streamHang struct {
	place *placement.Placement
}

func (n *streamHang) Setup(t *testing.T) []framework.Option {
	n.place = placement.New(t)

	return []framework.Option{
		framework.WithProcesses(n.place),
	}
}

func (n *streamHang) Run(t *testing.T, ctx context.Context) {
	n.place.WaitUntilRunning(t, ctx)
	numActors := 5000

	host1 := &v1pb.Host{
		Name:      "myapp1",
		Namespace: "default",
		Port:      1231,
		Entities:  getActorsList(numActors, "host1"),
		Id:        "myapp1",
		ApiLevel:  uint32(20),
	}

	ctx1, cancel1 := context.WithCancel(ctx)
	t.Cleanup(cancel1)
	stream1, streamCancel1 := n.getStream(t, ctx1)
	t.Cleanup(streamCancel1)
	err := stream1.Send(host1)
	require.NoError(t, err, "Failed to send host1")

	// Start reading from stream1
	var wg sync.WaitGroup
	wg.Add(1)
	updateCh1 := make(chan *v1pb.PlacementTables)
	go func() {
		defer wg.Done()
		for {
			resp, err := stream1.Recv()
			if err != nil {
				return
			}

			if resp.GetOperation() == "update" {
				updateCh1 <- resp.GetTables()
			}
		}
	}()

	// Wait for the first placement update
	select {
	case <-ctx.Done():
		t.Fatal("Test context canceled unexpectedly")
	case placementTables := <-updateCh1:
		require.Len(t, placementTables.GetEntries(), numActors)
		require.Contains(t, placementTables.GetEntries(), "host1-0")
	}

	// Set up host2 and stream2 (not reading from stream)
	host2 := &v1pb.Host{
		Name:      "myapp2",
		Namespace: "default",
		Port:      1232,
		Entities:  getActorsList(numActors, "host2"),
		Id:        "myapp2",
		ApiLevel:  uint32(20),
	}

	ctx2, cancel2 := context.WithCancel(ctx)
	t.Cleanup(cancel2)
	stream2, streamCancel2 := n.getStream(t, ctx2)
	t.Cleanup(streamCancel2)
	err = stream2.Send(host2)
	require.NoError(t, err, "Failed to send host2")

	// Intentionally not reading from stream2 to simulate a hang
	// The placement tables message that the Placement server will try to disseminate to stream2 will
	// contain 10000 actors and around 100Kb - higher than the default stream write buffer size of 32KBs
	// So around this moment of the test, we're expecting the stream2 to hang, but stream 1 should
	// still be receiving the disseminated message
	select {
	case <-ctx.Done():
		t.Fatal("Test context canceled unexpectedly")
	case placementTables := <-updateCh1:
		require.Len(t, placementTables.GetEntries(), numActors*2)
		require.Contains(t, placementTables.GetEntries(), "host1-0")
		require.Contains(t, placementTables.GetEntries(), "host2-0")
	}

	// Set up host3 and stream3 (reading from stream)
	host3 := &v1pb.Host{
		Name:      "myapp3",
		Namespace: "default",
		Port:      1233,
		Entities:  getActorsList(numActors, "host3"),
		Id:        "myapp3",
		ApiLevel:  uint32(20),
	}

	ctx3, cancel3 := context.WithCancel(ctx)
	t.Cleanup(cancel3)
	stream3, streamCancel3 := n.getStream(t, ctx3)
	t.Cleanup(streamCancel3)
	err = stream3.Send(host3)
	require.NoError(t, err, "Failed to send host3")

	// Start reading from stream3
	wg.Add(1)
	updateCh3 := make(chan *v1pb.PlacementTables)
	go func() {
		defer wg.Done()
		for {
			resp, err := stream3.Recv()
			if err != nil {
				return
			}

			if resp.GetOperation() == "update" {
				updateCh3 <- resp.GetTables()
			}
		}
	}()

	select {
	case <-ctx.Done():
		t.Fatal("Test context canceled unexpectedly")
	case placementTables := <-updateCh3:
		require.GreaterOrEqual(t, len(placementTables.GetEntries()), numActors*2)
		require.Contains(t, placementTables.GetEntries(), "host1-0")
		require.Contains(t, placementTables.GetEntries(), "host3-0")
		require.NotContains(t, placementTables.GetEntries(), "host2-0")
	case <-time.After(10 * time.Second):
		t.Log("Timeout waiting for placement update on stream3")
	}

	// Clean up
	streamCancel1()
	streamCancel2()
	streamCancel3()
	wg.Wait()
}

func (n *streamHang) getStream(t *testing.T, ctx context.Context) (v1pb.Placement_ReportDaprStatusClient, func()) {
	t.Helper()

	//nolint:staticcheck
	conn, err := grpc.DialContext(ctx, n.place.Address(),
		grpc.WithBlock(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	client := v1pb.NewPlacementClient(conn)
	ctx = metadata.AppendToOutgoingContext(ctx, "dapr-accept-vnodes", "false")

	stream, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)

	cancel := func() {
		stream.CloseSend()
		conn.Close()
	}

	return stream, cancel
}

func getActorsList(numActors int, prefix string) []string {
	var actors []string
	for i := range numActors {
		actors = append(actors, fmt.Sprintf("%s-%d", prefix, i))
	}
	return actors
}
