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

package namespacedActors

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	grpcinsecure "google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(namespacedActors))
}

type namespacedActors struct {
	place *placement.Placement
}

func (n *namespacedActors) Setup(t *testing.T) []framework.Option {
	n.place = placement.New(t)

	return []framework.Option{
		framework.WithProcesses(n.place),
	}
}

func (n *namespacedActors) Run(t *testing.T, ctx context.Context) {
	n.place.WaitUntilRunning(t, ctx)

	addr := n.place.Address()
	conn, err := grpc.DialContext(ctx, addr, grpc.WithBlock(), grpc.WithReturnConnectionError(),
		grpc.WithTransportCredentials(grpcinsecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	client := v1pb.NewPlacementClient(conn)
	ctx = metadata.AppendToOutgoingContext(ctx, "dapr-accept-vnodes", "false")

	t.Run("namespaces are disseminated properly", func(t *testing.T) {
		host1 := &v1pb.Host{
			Name:      "myapp1",
			Namespace: "ns1",
			Port:      1231,
			Entities:  []string{"actor1-ns1"},
			Id:        "myapp1",
			ApiLevel:  uint32(20),
		}
		host2 := &v1pb.Host{
			Name:      "myapp2",
			Namespace: "ns2",
			Port:      1232,
			Entities:  []string{"actor2-ns2", "actor3-ns2"},
			Id:        "myapp2",
			ApiLevel:  uint32(20),
		}
		host3 := &v1pb.Host{
			Name:      "myapp3",
			Namespace: "ns3",
			Port:      1233,
			Entities:  []string{"actor4-ns2", "actor5-ns2", "actor6-ns2"},
			Id:        "myapp3",
			ApiLevel:  uint32(20),
		}

		stream1, err := establishStream(t, ctx, client, host1)
		require.NoError(t, err)

		stream2, err := establishStream(t, ctx, client, host2)
		require.NoError(t, err)

		stream3, err := establishStream(t, ctx, client, host3)
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			th1, err := stream1.Recv()
			require.NoError(t, err)
			condition := false

			if th1.GetOperation() == "update" {
				// assert the placement table sent to host 1 in ns1
				// contains actors from all hosts
				// because all of them technically belong in the empty namespace
				require.Len(t, th1.GetTables().GetEntries(), 1)
				require.Contains(t, th1.GetTables().GetEntries(), "actor1-ns1")
				condition = true
			}
			return condition
		}, time.Second*10, time.Millisecond*10)

		require.Eventually(t, func() bool {
			th2, err := stream2.Recv()
			require.NoError(t, err)
			condition := false

			if th2.GetOperation() == "update" {
				if len(th2.GetTables().GetEntries()) != 5 {
					return false
				}
				fmt.Println(th2.GetTables().GetEntries())
				// assert the placement table sent to host 2 in ns2
				// contains actors from all hosts in ns2 (hosts 2 and 3)
				// but doesn't contain any actors from ns1
				require.Contains(t, th2.GetTables().GetEntries(), "actor2-ns2")
				require.Contains(t, th2.GetTables().GetEntries(), "actor3-ns2")
				require.Contains(t, th2.GetTables().GetEntries(), "actor4-ns2")
				require.Contains(t, th2.GetTables().GetEntries(), "actor5-ns2")
				require.Contains(t, th2.GetTables().GetEntries(), "actor6-ns2")
				condition = true
			}
			return condition
		}, time.Second*20, time.Millisecond*10)

		require.Eventually(t, func() bool {
			th3, err := stream3.Recv()
			require.NoError(t, err)
			condition := false

			if th3.GetOperation() == "update" {
				// assert the placement table sent to host 3 in ns2
				// contains actors from all hosts in ns2 (hosts 2 and 3)
				// but doesn't contain any actors from ns1
				require.Len(t, th3.GetTables().GetEntries(), 5)
				require.Contains(t, th3.GetTables().GetEntries(), "actor2-ns2")
				require.Contains(t, th3.GetTables().GetEntries(), "actor3-ns2")
				require.Contains(t, th3.GetTables().GetEntries(), "actor4-ns2")
				require.Contains(t, th3.GetTables().GetEntries(), "actor5-ns2")
				require.Contains(t, th3.GetTables().GetEntries(), "actor6-ns2")
				condition = true
			}
			return condition
		}, time.Second*10, time.Millisecond*10)

		// Disconnect host 3
		err = stream3.CloseSend()
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			th1, err := stream1.Recv()
			require.NoError(t, err)
			condition := false

			if th1.GetOperation() == "update" {
				// assert the placement table sent to host 1 in ns1
				// contains actors from all hosts
				// because all of them technically belong in the empty namespace
				require.Len(t, th1.GetTables().GetEntries(), 1)
				require.Contains(t, th1.GetTables().GetEntries(), "actor1-ns1")
				condition = true
			}
			return condition
		}, time.Second*10, time.Millisecond*10)

		require.Eventually(t, func() bool {
			th2, err := stream2.Recv()
			require.NoError(t, err)
			condition := false

			if th2.GetOperation() == "update" {
				// assert the placement table sent to host 2 in ns2
				// contains actors from all hosts in ns2 (now only hosts 2)
				// but doesn't contain any actors from ns1
				require.Len(t, th2.GetTables().GetEntries(), 5)
				require.Contains(t, th2.GetTables().GetEntries(), "actor2-ns2")
				require.Contains(t, th2.GetTables().GetEntries(), "actor3-ns2")
				condition = true
			}
			return condition
		}, time.Second*10, time.Millisecond*10)
	})

	// old sidecars = pre 1.14
	//t.Run("namespaces are disseminated properly to old sidecars", func(t *testing.T) {
	//	host1 := &v1pb.Host{
	//		Name:     "myapp1",
	//		Port:     1231,
	//		Entities: []string{"actor1-ns1"},
	//		Id:       "myapp1",
	//		ApiLevel: uint32(20),
	//	}
	//	host2 := &v1pb.Host{
	//		Name:     "myapp2",
	//		Port:     1232,
	//		Entities: []string{"actor2-ns2", "actor3-ns2"},
	//		Id:       "myapp1",
	//		ApiLevel: uint32(20),
	//	}
	//	host3 := &v1pb.Host{
	//		Name:     "myapp3",
	//		Port:     1233,
	//		Entities: []string{"actor4-ns2", "actor5-ns2", "actor6-ns2"},
	//		Id:       "myapp1",
	//		ApiLevel: uint32(20),
	//	}
	//
	//	stream1, err := establishStream(t, ctx, client, host1)
	//	require.NoError(t, err)
	//
	//	stream2, err := establishStream(t, ctx, client, host2)
	//	require.NoError(t, err)
	//
	//	stream3, err := establishStream(t, ctx, client, host3)
	//	require.NoError(t, err)
	//
	//	require.Eventually(t, func() bool {
	//		th1, err := stream1.Recv()
	//		require.NoError(t, err)
	//		condition := false
	//
	//		if th1.GetOperation() == "update" {
	//			// assert the placement table sent to host 1 in ns1
	//			// contains actors from all hosts
	//			// because all of them technically belong in the empty namespace
	//			require.Len(t, th1.GetTables().GetEntries(), 6)
	//			require.Contains(t, th1.GetTables().GetEntries(), "actor1-ns1")
	//			require.Contains(t, th1.GetTables().GetEntries(), "actor2-ns2")
	//			require.Contains(t, th1.GetTables().GetEntries(), "actor3-ns2")
	//			require.Contains(t, th1.GetTables().GetEntries(), "actor4-ns2")
	//			require.Contains(t, th1.GetTables().GetEntries(), "actor5-ns2")
	//			require.Contains(t, th1.GetTables().GetEntries(), "actor6-ns2")
	//			condition = true
	//		}
	//		return condition
	//	}, time.Second*10, time.Millisecond*10)
	//
	//	require.Eventually(t, func() bool {
	//		th2, err := stream2.Recv()
	//		require.NoError(t, err)
	//		condition := false
	//
	//		if th2.GetOperation() == "update" {
	//			// assert the placement table sent to host 2 in ns2
	//			// contains actors from all hosts in ns2 (hosts 2 and 3)
	//			// but doesn't contain any actors from ns1
	//			require.Len(t, th2.GetTables().GetEntries(), 6)
	//			require.Contains(t, th2.GetTables().GetEntries(), "actor1-ns1")
	//			require.Contains(t, th2.GetTables().GetEntries(), "actor2-ns2")
	//			require.Contains(t, th2.GetTables().GetEntries(), "actor3-ns2")
	//			require.Contains(t, th2.GetTables().GetEntries(), "actor4-ns2")
	//			require.Contains(t, th2.GetTables().GetEntries(), "actor5-ns2")
	//			require.Contains(t, th2.GetTables().GetEntries(), "actor6-ns2")
	//			condition = true
	//		}
	//		return condition
	//	}, time.Second*10, time.Millisecond*10)
	//
	//	require.Eventually(t, func() bool {
	//		th3, err := stream3.Recv()
	//		require.NoError(t, err)
	//		condition := false
	//
	//		if th3.GetOperation() == "update" {
	//			// assert the placement table sent to host 3 in ns2
	//			// contains actors from all hosts in ns2 (hosts 2 and 3)
	//			// but doesn't contain any actors from ns1
	//			require.Len(t, th3.GetTables().GetEntries(), 6)
	//			require.Contains(t, th3.GetTables().GetEntries(), "actor1-ns1")
	//			require.Contains(t, th3.GetTables().GetEntries(), "actor2-ns2")
	//			require.Contains(t, th3.GetTables().GetEntries(), "actor3-ns2")
	//			require.Contains(t, th3.GetTables().GetEntries(), "actor4-ns2")
	//			require.Contains(t, th3.GetTables().GetEntries(), "actor5-ns2")
	//			require.Contains(t, th3.GetTables().GetEntries(), "actor6-ns2")
	//			condition = true
	//		}
	//		return condition
	//	}, time.Second*10, time.Millisecond*10)
	//
	//	// Disconnect host 3
	//	err = stream3.CloseSend()
	//	require.NoError(t, err)
	//
	//	require.Eventually(t, func() bool {
	//		th1, err := stream1.Recv()
	//		require.NoError(t, err)
	//		condition := false
	//
	//		if th1.GetOperation() == "update" {
	//			// assert the placement table sent to host 1 in ns1
	//			// contains actors from all hosts
	//			// because all of them technically belong in the empty namespace
	//			require.Len(t, th1.GetTables().GetEntries(), 1)
	//			require.Contains(t, th1.GetTables().GetEntries(), "actor1-ns1")
	//			condition = true
	//		}
	//		return condition
	//	}, time.Second*10, time.Millisecond*10)
	//
	//	//require.Eventually(t, func() bool {
	//	//	th2, err := stream2.Recv()
	//	//	require.NoError(t, err)
	//	//	condition := false
	//	//
	//	//	if th2.GetOperation() == "update" {
	//	//		// assert the placement table sent to host 2 in ns2
	//	//		// contains actors from all hosts in ns2 (now only hosts 2)
	//	//		// but doesn't contain any actors from ns1
	//	//		require.Len(t, th2.GetTables().GetEntries(), 5)
	//	//		require.Contains(t, th2.GetTables().GetEntries(), "actor2-ns2")
	//	//		require.Contains(t, th2.GetTables().GetEntries(), "actor3-ns2")
	//	//		condition = true
	//	//	}
	//	//	return condition
	//	//}, time.Second*10, time.Millisecond*10)
	//})
}

func establishStream(t *testing.T, ctx context.Context, client v1pb.PlacementClient, firstMessage *v1pb.Host) (v1pb.Placement_ReportDaprStatusClient, error) {
	t.Helper()
	var stream v1pb.Placement_ReportDaprStatusClient
	var err error

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		stream, err = client.ReportDaprStatus(ctx)
		if err != nil {
			return
		}

		err = stream.Send(firstMessage)
		if err != nil {
			return
		}
		_, err = stream.Recv()
	}, time.Second*5, time.Millisecond*10)
	return stream, err
}
