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

package ha

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(failover))
}

type failover struct {
	fp         *ports.Ports
	placements []*placement.Placement
	srv        *prochttp.HTTP
}

func (n *failover) Setup(t *testing.T) []framework.Option {
	n.fp = ports.Reserve(t, 3)
	port1, port2, port3 := n.fp.Port(t), n.fp.Port(t), n.fp.Port(t)
	opts := []placement.Option{
		placement.WithInitialCluster(fmt.Sprintf("p1=localhost:%d,p2=localhost:%d,p3=localhost:%d", port1, port2, port3)),
		placement.WithInitialClusterPorts(port1, port2, port3),
	}
	n.placements = []*placement.Placement{
		placement.New(t, append(opts, placement.WithID("p1"))...),
		placement.New(t, append(opts, placement.WithID("p2"))...),
		placement.New(t, append(opts, placement.WithID("p3"))...),
	}

	// Dummy http server that returns the list of actors
	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		types := []string{"actor1"}
		fmt.Fprintf(w, `{"entities": ["%s"]}`, strings.Join(types, `","`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`OK1`))
	})
	n.srv = prochttp.New(t, prochttp.WithHandler(handler))

	return []framework.Option{
		framework.WithProcesses(n.fp, n.srv, n.placements[0], n.placements[1], n.placements[2]),
	}
}

func (n *failover) Run(t *testing.T, ctx context.Context) {
	for _, p := range n.placements {
		p.WaitUntilRunning(t, ctx)
	}

	host1 := &v1pb.Host{
		Name:      "myapp1",
		Namespace: "ns1",
		Port:      1231,
		Entities:  []string{"actor1", "actor10"},
		Id:        "myapp1",
		ApiLevel:  uint32(20),
	}
	host2 := &v1pb.Host{
		Name:      "myapp2",
		Namespace: "ns2",
		Port:      1232,
		Entities:  []string{"actor2", "actor3"},
		Id:        "myapp2",
		ApiLevel:  uint32(20),
	}
	host3 := &v1pb.Host{
		Name:      "myapp3",
		Namespace: "ns2",
		Port:      1233,
		Entities:  []string{"actor4", "actor5", "actor6", "actor10"},
		Id:        "myapp3",
		ApiLevel:  uint32(20),
	}

	leaderIndex := n.getLeader(t, ctx, -1)

	placementMessageCh1 := n.placements[leaderIndex].RegisterHost(t, ctx, host1)
	placementMessageCh2 := n.placements[leaderIndex].RegisterHost(t, ctx, host2)
	placementMessageCh3 := n.placements[leaderIndex].RegisterHost(t, ctx, host3)

	// Dissemination is done properly on host 1
	select {
	case <-ctx.Done():
		return
	case placementTables := <-placementMessageCh1:
		if ctx.Err() != nil {
			return
		}

		require.Len(t, placementTables.GetEntries(), 2)
		require.Contains(t, placementTables.GetEntries(), "actor1")
		require.Contains(t, placementTables.GetEntries(), "actor10")

		entry, ok := placementTables.GetEntries()["actor10"]
		require.True(t, ok)
		loadMap := entry.GetLoadMap()
		assert.Len(t, loadMap, 1)
		assert.Contains(t, loadMap, host1.GetName())
	}

	// Dissemination is done properly on host 2
	msgCnt := 0
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		select {
		case <-ctx.Done():
			return
		case placementTables := <-placementMessageCh2:
			if ctx.Err() != nil {
				return
			}

			msgCnt++
			assert.Len(c, placementTables.GetEntries(), 6)
			assert.Contains(c, placementTables.GetEntries(), "actor2")
			assert.Contains(c, placementTables.GetEntries(), "actor3")
			assert.Contains(c, placementTables.GetEntries(), "actor4")
			assert.Contains(c, placementTables.GetEntries(), "actor5")
			assert.Contains(c, placementTables.GetEntries(), "actor6")
			assert.Contains(c, placementTables.GetEntries(), "actor10")

			entry, ok := placementTables.GetEntries()["actor10"]
			if assert.True(c, ok) {
				loadMap := entry.GetLoadMap()
				assert.Len(c, loadMap, 1)
				assert.Contains(c, loadMap, host3.GetName())
			}
		}

		assert.GreaterOrEqual(c, msgCnt, 1)
	}, 10*time.Second, 10*time.Millisecond)

	// Dissemination is done properly on host 3
	msgCnt = 0
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		select {
		case <-ctx.Done():
			return
		case placementTables := <-placementMessageCh3:
			if ctx.Err() != nil {
				return
			}

			msgCnt++
			assert.Len(c, placementTables.GetEntries(), 6)
			assert.Contains(c, placementTables.GetEntries(), "actor2")
			assert.Contains(c, placementTables.GetEntries(), "actor3")
			assert.Contains(c, placementTables.GetEntries(), "actor4")
			assert.Contains(c, placementTables.GetEntries(), "actor5")
			assert.Contains(c, placementTables.GetEntries(), "actor6")
			assert.Contains(c, placementTables.GetEntries(), "actor10")

			entry, ok := placementTables.GetEntries()["actor10"]
			if assert.True(c, ok) {
				loadMap := entry.GetLoadMap()
				assert.Len(c, loadMap, 1)
				assert.Contains(c, loadMap, host3.GetName())
			}
		}
		assert.GreaterOrEqual(c, msgCnt, 1)
	}, 10*time.Second, 10*time.Millisecond)

	// Stop the placement leader and don't reconnect one of the hosts in ns2. Check that:
	// - a new leader has been elected
	// - dissemination message hasn't been sent to host1, because there haven't been changes in ns1
	// - dissemination message has been sent to host2, because host 3 hasn't reconnected, thus
	// 	there have been changes in the dissemination table
	n.placements[leaderIndex].Cleanup(t)

	leaderIndex = n.getLeader(t, ctx, leaderIndex)

	placementMessageCh2 = n.placements[leaderIndex].RegisterHost(t, ctx, host2)
	n.placements[leaderIndex].RegisterHost(t, ctx, host1)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		select {
		case <-ctx.Done():
			return
		case placementTables := <-placementMessageCh2:
			if ctx.Err() != nil {
				return
			}

			msgCnt++
			assert.Len(t, placementTables.GetEntries(), 2)
			assert.Contains(t, placementTables.GetEntries(), "actor2")
			assert.Contains(t, placementTables.GetEntries(), "actor3")
		}
		assert.GreaterOrEqual(c, msgCnt, 1)
	}, 10*time.Second, 500*time.Millisecond)
}

func (n *failover) getLeader(t *testing.T, ctx context.Context, skip int) int {
	// Connect to each placement until one succeeds, indicating that a leader has been elected.
	// If the condition is met, j is the index of the placement leader
	var stream v1pb.Placement_ReportDaprStatusClient

	i := -1
	j := 0
	require.Eventually(t, func() bool {
		i++
		j = i % len(n.placements)
		if j == skip {
			return false
		}

		host := n.placements[j].Address()
		//nolint:staticcheck
		conn, err := grpc.DialContext(ctx, host, grpc.WithBlock(), grpc.WithReturnConnectionError(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err != nil {
			return false
		}
		t.Cleanup(func() { require.NoError(t, conn.Close()) })

		client := v1pb.NewPlacementClient(conn)

		stream, err = client.ReportDaprStatus(ctx)
		defer stream.CloseSend()
		if err != nil {
			return false
		}

		// The dummy host doesn't host any actors, so it won't affect the placement table
		err = stream.Send(&v1pb.Host{
			ApiLevel: uint32(20),
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

	return j
}
