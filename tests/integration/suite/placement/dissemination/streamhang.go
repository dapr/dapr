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
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
	if runtime.GOOS == "windows" {
		t.Skip("Sending big messages is unpredictable on Windows CI runners")
	}

	n.place = placement.New(t)

	return []framework.Option{
		framework.WithProcesses(n.place),
	}
}

func (n *streamHang) Run(t *testing.T, ctx context.Context) {
	n.place.WaitUntilRunning(t, ctx)

	// Set up host1 and stream1 (not reading from stream)
	host1 := &v1pb.Host{
		Name:      "local/myapp1",
		Namespace: "default",
		Port:      1232,
		Entities:  getActorsList(9000, "host1"),
		Id:        "myapp1",
		ApiLevel:  uint32(20),
	}

	ctx1, cancel1 := context.WithCancel(ctx)
	t.Cleanup(cancel1)
	var err error
	var stream1 v1pb.Placement_ReportDaprStatusClient
	var streamCancel1 context.CancelFunc
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		stream1, streamCancel1 = n.getStream(c, ctx1)
		t.Cleanup(streamCancel1)
		if stream1 != nil {
			err = stream1.Send(host1)
			assert.NoError(c, err, "Failed to send host1")
		}
	}, 10*time.Second, 10*time.Millisecond)

	// Intentionally not reading from stream1 to simulate a hang on the larger message after host 1 connects

	time.Sleep(2 * time.Second) // Wait until first dissemination is triggered

	host2 := &v1pb.Host{
		Name:      "local/myapp2",
		Namespace: "default",
		Port:      1231,
		Entities:  getActorsList(10, "host2"),
		Id:        "myapp2",
		ApiLevel:  uint32(20),
	}

	ctx2, cancel2 := context.WithCancel(ctx)
	t.Cleanup(cancel2)
	var stream2 v1pb.Placement_ReportDaprStatusClient
	var streamCancel2 context.CancelFunc
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		stream2, streamCancel2 = n.getStream(c, ctx2)
		t.Cleanup(streamCancel2)
		if stream2 != nil {
			err = stream2.Send(host2)
			assert.NoError(c, err, "Failed to send host2")
		}
	}, 10*time.Second, 10*time.Millisecond)

	// Start reading from stream2
	updateCh2 := make(chan *v1pb.PlacementTables, 5)
	go func() {
		defer close(updateCh2)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			resp, err2 := stream2.Recv()
			if err2 != nil {
				break
			}

			if resp.GetOperation() == "update" {
				select {
				case <-ctx.Done():
					return
				case updateCh2 <- resp.GetTables():
				}
			}
		}
	}()

	// The placement tables message that the Placement server will try to disseminate to stream1 will
	// contain 9001 actors and around 90Kb - higher than the default stream write buffer size of 32KBs
	// So around this moment of the test, we're expecting the stream1 to hang and subsequently get removed
	// Then stream 2 should receive a new dissemination message containing only the actors from host2
	// Because of the retry mechanism on the placement side (3x1sec) plus the dissemination window
	// this takes a while
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		placementTables, ok := <-updateCh2
		require.True(t, ok)
		assert.Len(c, placementTables.GetEntries(), 10)
		assert.Contains(c, placementTables.GetEntries(), "host2-0")
	}, 30*time.Second, 100*time.Millisecond)

	time.Sleep(1 * time.Second) // Wait until first dissemination is triggered

	// Set up host3 and stream3 (reading from stream)
	host3 := &v1pb.Host{
		Name:      "local/myapp3",
		Namespace: "default",
		Port:      1233,
		Entities:  getActorsList(3, "host3"),
		Id:        "myapp3",
		ApiLevel:  uint32(20),
	}

	ctx3, cancel3 := context.WithCancel(ctx)
	t.Cleanup(cancel3)
	var stream3 v1pb.Placement_ReportDaprStatusClient
	var streamCancel3 context.CancelFunc
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		stream3, streamCancel3 = n.getStream(c, ctx3)
		t.Cleanup(streamCancel3)
		if stream3 != nil {
			err = stream3.Send(host3)
			assert.NoError(c, err, "Failed to send host3")
		}
	}, 10*time.Second, 10*time.Millisecond)

	err = stream2.Send(host2)
	require.NoError(t, err, "Failed to send host2")

	// Start reading from stream3
	updateCh3 := make(chan *v1pb.PlacementTables)
	go func() {
		defer close(updateCh3)
		for {
			select {
			case <-ctx.Done():
				break
			default:
			}

			resp, err := stream3.Recv()
			if err != nil {
				break
			}

			if resp.GetOperation() == "update" {
				select {
				case <-ctx.Done():
					break
				case updateCh3 <- resp.GetTables():
				}
			}
		}
	}()

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		placementTables, ok := <-updateCh3
		require.True(t, ok)
		assert.Len(c, placementTables.GetEntries(), 13)
		assert.Contains(c, placementTables.GetEntries(), "host2-0")
		assert.Contains(c, placementTables.GetEntries(), "host3-0")
		assert.NotContains(c, placementTables.GetEntries(), "host1-0")
	}, 15*time.Second, 500*time.Millisecond)

	// Clean up
	streamCancel1()
	streamCancel2()
	streamCancel3()
}

func (n *streamHang) getStream(t assert.TestingT, ctx context.Context) (v1pb.Placement_ReportDaprStatusClient, func()) {
	//nolint:staticcheck
	conn, err := grpc.DialContext(ctx, n.place.Address(),
		grpc.WithBlock(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	//nolint:testifylint
	if !assert.NoError(t, err) {
		return nil, func() {}
	}
	client := v1pb.NewPlacementClient(conn)
	ctx = metadata.AppendToOutgoingContext(ctx, "dapr-accept-vnodes", "false")

	stream, err := client.ReportDaprStatus(ctx)
	if !assert.NoError(t, err) {
		return nil, func() {}
	}

	cancel := func() {
		stream.CloseSend()
		conn.Close()
	}

	return stream, cancel
}

func getActorsList(numActors int, prefix string) []string {
	actors := make([]string, 0, numActors)
	for i := range numActors {
		actors = append(actors, fmt.Sprintf("%s-%d", prefix, i))
	}
	return actors
}
