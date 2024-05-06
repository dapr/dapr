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

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(namespacedActors))
}

type namespacedActors struct {
	places []*placement.Placement
}

func (v *namespacedActors) Setup(t *testing.T) []framework.Option {
	fp := ports.Reserve(t, 3)
	port1, port2, port3 := fp.Port(t), fp.Port(t), fp.Port(t)
	opts := []placement.Option{
		placement.WithInitialCluster(fmt.Sprintf("p1=localhost:%d,p2=localhost:%d,p3=localhost:%d", port1, port2, port3)),
		placement.WithInitialClusterPorts(port1, port2, port3),
	}
	v.places = []*placement.Placement{
		placement.New(t, append(opts, placement.WithID("p1"))...),
		placement.New(t, append(opts, placement.WithID("p2"))...),
		placement.New(t, append(opts, placement.WithID("p3"))...),
	}

	return []framework.Option{
		framework.WithProcesses(fp, v.places[0], v.places[1], v.places[2]),
	}
}

func (v *namespacedActors) Run(t *testing.T, ctx context.Context) {
	v.places[0].WaitUntilRunning(t, ctx)
	v.places[1].WaitUntilRunning(t, ctx)
	v.places[2].WaitUntilRunning(t, ctx)

	//var stream v1pb.Placement_ReportDaprStatusClient
	var stream1, stream2, stream3 v1pb.Placement_ReportDaprStatusClient
	var client v1pb.PlacementClient

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
		Id:        "myapp1",
		ApiLevel:  uint32(20),
	}
	host3 := &v1pb.Host{
		Name:      "myapp3",
		Namespace: "ns2",
		Port:      1233,
		Entities:  []string{"actor4-ns2", "actor5-ns2", "actor6-ns2"},
		Id:        "myapp1",
		ApiLevel:  uint32(20),
	}

	// Try connecting to different placement servers, until we find the leader
	for i := 0; i < 3; i++ {
		j := -1
		require.Eventually(t, func() bool {
			j++
			if j >= 3 {
				j = 0
			}
			host := v.places[j].Address()
			conn, err := grpc.DialContext(ctx, host, grpc.WithBlock(), grpc.WithReturnConnectionError(),
				grpc.WithTransportCredentials(grpcinsecure.NewCredentials()),
			)
			if err != nil {
				return false
			}
			t.Cleanup(func() { require.NoError(t, conn.Close()) })

			client = v1pb.NewPlacementClient(conn)
			stream1, err = client.ReportDaprStatus(ctx)
			if err != nil {
				return false
			}

			err = stream1.Send(host1)
			if err != nil {
				return false
			}
			_, err = stream1.Recv()
			if err != nil {
				return false
			}
			return true
		}, time.Second*10, time.Millisecond*10)
	}

	t.Run("namespaces are disseminated properly", func(t *testing.T) {
		var err error

		stream2, err = client.ReportDaprStatus(ctx)
		require.NoError(t, err)
		err = stream2.Send(host2)
		require.NoError(t, err)

		stream3, err = client.ReportDaprStatus(ctx)
		require.NoError(t, err)
		err = stream3.Send(host3)
		require.NoError(t, err)

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			th1, err := stream1.Recv()
			require.NoError(t, err)

			th2, err := stream2.Recv()
			require.NoError(t, err)

			th3, err := stream3.Recv()
			require.NoError(t, err)

			assert.Equal(c, "update", th1.GetOperation())
			if assert.NotNil(c, th1.GetTables()) {
				// assert the placement table sent to host 1 in ns1
				// only contains actors from ns 1 (host 1)
				// but doesn't contain any actors from ns2
				assert.Len(c, th1.GetTables().GetEntries(), 1)
				assert.Contains(c, th1.GetTables().GetEntries(), "actor1-ns1")
				assert.NotContains(c, th1.GetTables().GetEntries(), "actor2-ns2")
				assert.NotContains(c, th1.GetTables().GetEntries(), "actor3-ns2")
				assert.NotContains(c, th1.GetTables().GetEntries(), "actor4-ns2")
				assert.NotContains(c, th1.GetTables().GetEntries(), "actor5-ns2")
				assert.NotContains(c, th1.GetTables().GetEntries(), "actor6-ns2")

				// assert the placement table sent to host 2 in ns2
				// contains actors from all hosts in ns2 (hosts 2 and 3)
				// but doesn't contain any actors from ns1
				assert.Len(c, th2.GetTables().GetEntries(), 5)
				assert.Contains(c, th2.GetTables().GetEntries(), "actor2-ns2")
				assert.Contains(c, th2.GetTables().GetEntries(), "actor3-ns2")
				assert.Contains(c, th2.GetTables().GetEntries(), "actor4-ns2")
				assert.Contains(c, th2.GetTables().GetEntries(), "actor5-ns2")
				assert.Contains(c, th2.GetTables().GetEntries(), "actor6-ns2")
				assert.NotContains(c, th2.GetTables().GetEntries(), "actor1-ns1")

				// assert the placement table sent to host 3 in ns2
				// contains actors from all hosts in ns2 (hosts 2 and 3)
				// but doesn't contain any actors from ns1
				assert.Len(c, th3.GetTables().GetEntries(), 5)
				assert.Contains(c, th3.GetTables().GetEntries(), "actor2-ns2")
				assert.Contains(c, th3.GetTables().GetEntries(), "actor3-ns2")
				assert.Contains(c, th3.GetTables().GetEntries(), "actor4-ns2")
				assert.Contains(c, th3.GetTables().GetEntries(), "actor5-ns2")
				assert.Contains(c, th3.GetTables().GetEntries(), "actor6-ns2")
				assert.NotContains(c, th3.GetTables().GetEntries(), "actor1-ns1")
			}
		}, time.Second*20, time.Millisecond*10)
	})

}
