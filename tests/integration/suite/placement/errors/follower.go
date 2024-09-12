/*
Copyright 2023 The Dapr Authors
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

package errors

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcinsecure "google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	_ "github.com/dapr/dapr/pkg/placement"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(follower))
}

// follower tests placement can find quorum with tls disabled.
type follower struct {
	places  []*placement.Placement
}

func (n *follower) Setup(t *testing.T) []framework.Option {
	fp := ports.Reserve(t, 3)
	port1, port2, port3 := fp.Port(t), fp.Port(t), fp.Port(t)
	opts := []placement.Option{
		placement.WithInitialCluster(fmt.Sprintf("p1=localhost:%d,p2=localhost:%d,p3=localhost:%d", port1, port2, port3)),
		placement.WithInitialClusterPorts(port1, port2, port3),
	}
	n.places = []*placement.Placement{
		placement.New(t, append(opts, placement.WithID("p1"))...),
		placement.New(t, append(opts, placement.WithID("p2"))...),
		placement.New(t, append(opts, placement.WithID("p3"))...),
	}

	return []framework.Option{
		framework.WithProcesses(fp, n.places[0], n.places[1], n.places[2]),
	}
}

func (n *follower) Run(t *testing.T, ctx context.Context) {
	n.places[0].WaitUntilRunning(t, ctx)
	n.places[1].WaitUntilRunning(t, ctx)
	n.places[2].WaitUntilRunning(t, ctx)

	var stream v1pb.Placement_ReportDaprStatusClient

	// Try connecting to each placement until one succeeds,
	// indicating that a leader has been elected
	j := -1
	require.Eventually(t, func() bool {
		j++
		if j >= 3 {
			j = 0
		}
		leaderHost := n.places[j].Address()
		//nolint:staticcheck
		conn, err := grpc.DialContext(ctx, leaderHost, grpc.WithBlock(), grpc.WithReturnConnectionError(),
			grpc.WithTransportCredentials(grpcinsecure.NewCredentials()),
		)
		if err != nil {
			return false
		}
		t.Cleanup(func() { require.NoError(t, conn.Close()) })
		client := v1pb.NewPlacementClient(conn)

		stream, err = client.ReportDaprStatus(ctx)
		if err != nil {
			return false
		}
		err = stream.Send(&v1pb.Host{
			Id: "app-1",
		})
		if err != nil {
			return false
		}
		_, err = stream.Recv()
		if err != nil {
			return false
		}
		return true
	}, time.Second*10, time.Millisecond*10)

	// Now we know that the leader is j, so we try to connect to a follower to check the error
	followerHost := n.places[(j+1)%3].Address()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		//nolint:staticcheck
		conn, err := grpc.DialContext(ctx, followerHost, grpc.WithBlock(), grpc.WithReturnConnectionError(),
			grpc.WithTransportCredentials(grpcinsecure.NewCredentials()),
		)
		t.Cleanup(func() { require.NoError(t, conn.Close()) })
		//nolint:testifylint
		if assert.NoError(c, err) {
			client := v1pb.NewPlacementClient(conn)

			stream, err := client.ReportDaprStatus(ctx)
			if assert.NoError(c, err) {
				err = stream.Send(&v1pb.Host{
					Id: "app-2",
				})
				//nolint:testifylint
				assert.NoError(c, err)

				_, err = stream.Recv()
				//nolint:testifylint
				assert.Error(c, err)
			}
		}
	}, time.Second*10, time.Millisecond*100)

	// Test that the placement protocol correctly handles a non-existent host
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		// Attempt to connect to a follower
		followerHost := n.places[(j+1)%3].Address()
		//nolint:staticcheck
		conn, err := grpc.DialContext(ctx, followerHost, grpc.WithBlock(), grpc.WithReturnConnectionError(),
			grpc.WithTransportCredentials(grpcinsecure.NewCredentials()),
		)
		t.Cleanup(func() { require.NoError(t, conn.Close()) })

		if assert.NoError(c, err) {
			client := v1pb.NewPlacementClient(conn)

			// Attempt to report status for a non-existent host
			stream, err := client.ReportDaprStatus(ctx)
			if assert.NoError(c, err) {
				err = stream.Send(&v1pb.Host{
					Id: "foo",
				})
				//nolint:testifylint
				assert.NoError(c, err)

				// Expecting a "not found" error when receiving a response
				_, err = stream.Recv()
				assert.Error(c, err)
				assert.Equal(c, codes.NotFound, status.Code(err)) 
			}
		}
	}, time.Second*10, time.Millisecond*100)

	// New test for handling a "not found" error
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		followerHost := n.places[(j+1)%3].Address()
		//nolint:staticcheck
		conn, err := grpc.DialContext(ctx, followerHost, grpc.WithBlock(), grpc.WithReturnConnectionError(),
			grpc.WithTransportCredentials(grpcinsecure.NewCredentials()),
		)
		t.Cleanup(func() { require.NoError(t, conn.Close()) })

		if assert.NoError(c, err) {
			client := v1pb.NewPlacementClient(conn)

			// Attempt to create a host that already exists
			stream, err := client.ReportDaprStatus(ctx)
			if assert.NoError(c, err) {
				err = stream.Send(&v1pb.Host{
					Id: "app-1", // Assuming "app-1" already exists
				})
				assert.NoError(c, err)

				// Attempt to send the same host again to trigger "Already Exists"
				err = stream.Send(&v1pb.Host{
					Id: "app-1",
				})
				//nolint:testifylint
				assert.NoError(c, err)

				// Expecting an "already exists" error when receiving a response
				_, err = stream.Recv()
				assert.Error(c, err)
				assert.Equal(c, codes.AlreadyExists, status.Code(err)) 
			}
		}
	}, time.Second*10, time.Millisecond*100)

	// Test for "Permission Denied" error
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		followerHost := n.places[(j+1)%3].Address()
		//nolint:staticcheck
		conn, err := grpc.DialContext(ctx, followerHost, grpc.WithBlock(), grpc.WithReturnConnectionError(),
			grpc.WithTransportCredentials(grpcinsecure.NewCredentials()),
		)
		t.Cleanup(func() { require.NoError(t, conn.Close()) })

		if assert.NoError(c, err) {
			client := v1pb.NewPlacementClient(conn)

			// Attempt to create a host with an invalid ID to trigger "Permission Denied"
			stream, err := client.ReportDaprStatus(ctx)
			if assert.NoError(c, err) {
				err = stream.Send(&v1pb.Host{
					Id: "invalid-app-id", // Assuming this ID is invalid for the test
				})
				//nolint:testifylint
				assert.NoError(c, err)

				// Expecting a "permission denied" error when receiving a response
				_, err = stream.Recv()
				assert.Error(c, err)
				assert.Equal(c, codes.PermissionDenied, status.Code(err)) 
			}
		}
	}, time.Second*10, time.Millisecond*100)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		followerHost := n.places[(j+1)%3].Address()
		//nolint:staticcheck
		conn, err := grpc.DialContext(ctx, followerHost, grpc.WithBlock(), grpc.WithReturnConnectionError(),
			grpc.WithTransportCredentials(grpcinsecure.NewCredentials()),
		)
		t.Cleanup(func() { require.NoError(t, conn.Close()) })

		if assert.NoError(c, err) {
			client := v1pb.NewPlacementClient(conn)

			// Attempt to report status for a non-existent host
			stream, err := client.ReportDaprStatus(ctx)
			if assert.NoError(c, err) {
				err = stream.Send(&v1pb.Host{
					Id: "non-existent-app",
				})
				assert.NoError(c, err)

				// Expecting a "not found" error when receiving a response
				_, err = stream.Recv()
				assert.Error(c, err)
				assert.Equal(c, codes.NotFound, status.Code(err))
			}
		}
	}, time.Second*10, time.Millisecond*100)

	// Test for "Invalid Argument" error
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		followerHost := n.places[(j+1)%3].Address()
		//nolint:staticcheck
		conn, err := grpc.DialContext(ctx, followerHost, grpc.WithBlock(), grpc.WithReturnConnectionError(),
			grpc.WithTransportCredentials(grpcinsecure.NewCredentials()),
		)
		t.Cleanup(func() { require.NoError(t, conn.Close()) })

		if assert.NoError(c, err) {
			client := v1pb.NewPlacementClient(conn)

			// Attempt to send a malformed host ID (e.g., empty ID)
			stream, err := client.ReportDaprStatus(ctx)
			if assert.NoError(c, err) {
				err = stream.Send(&v1pb.Host{
					Id: "", // Invalid host ID
				})
				assert.NoError(c, err)

				// Expecting an "invalid argument" error when receiving a response
				_, err = stream.Recv()
				assert.Error(c, err)
				assert.Equal(c, codes.InvalidArgument, status.Code(err)) 
			}
		}
	}, time.Second*10, time.Millisecond*100)

	// Test for "Unavailable" error
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		followerHost := n.places[(j+1)%3].Address()
		//nolint:staticcheck
		conn, err := grpc.DialContext(ctx, followerHost, grpc.WithBlock(), grpc.WithReturnConnectionError(),
			grpc.WithTransportCredentials(grpcinsecure.NewCredentials()),
		)
		t.Cleanup(func() { require.NoError(t, conn.Close()) })

		if assert.NoError(c, err) {
			client := v1pb.NewPlacementClient(conn)

			// Simulate service unavailability by shutting down the follower
			n.places[(j+1)%3].Cleanup(t)

			// Attempt to report status for a valid host
			stream, err := client.ReportDaprStatus(ctx)
			if assert.NoError(c, err) {
				err = stream.Send(&v1pb.Host{
					Id: "app-1",
				})
				assert.NoError(c, err)

				// Expecting an "unavailable" error when receiving a response
				_, err = stream.Recv()
				assert.Error(c, err)
				assert.Equal(c, codes.Unavailable, status.Code(err))
			}
		}
	}, time.Second*10, time.Millisecond*100)
}
