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

package placement

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/cluster"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(failover))
}

// failover asserts placement survives losing its leader: the leader
// shutting down closes its live report stream telling the sidecar placement
// is shutting down, the remaining schedulers elect a new leader which
// rebuilds its table from the host that reconnects, and the revived
// original retakes the election, closing the interim leader's streams with
// FailedPrecondition.
type failover struct {
	cluster *cluster.Cluster
}

func (f *failover) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	f.cluster = cluster.New(t,
		cluster.WithCount(3),
		cluster.WithSchedulerOptions(scheduler.WithPlacementEnabled(true)),
	)
	return []framework.Option{
		framework.WithProcesses(f.cluster),
	}
}

func (f *failover) Run(t *testing.T, ctx context.Context) {
	f.cluster.WaitUntilRunning(t, ctx)

	openJobs := func(client schedulerv1pb.SchedulerClient) {
		stream, err := client.WatchJobs(ctx)
		require.NoError(t, err)
		require.NoError(t, stream.Send(&schedulerv1pb.WatchJobsRequest{
			WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Initial{Initial: &schedulerv1pb.WatchJobsRequestInitial{
				AppId:                      "capable-sidecar",
				Namespace:                  "default",
				SupportsSchedulerPlacement: true,
			}},
		}))
	}

	clients := make([]schedulerv1pb.SchedulerClient, 3)
	for n := range 3 {
		clients[n] = f.cluster.ClientN(t, ctx, n)
		openJobs(clients[n])
	}

	// leaderIn returns the single advertised leader once the client's
	// scheduler broadcasts hostCount hosts.
	leaderIn := func(client schedulerv1pb.SchedulerClient, hostCount int) string {
		var addr string
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			hstream, err := client.WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
			if !assert.NoError(c, err) {
				return
			}
			//nolint:errcheck
			defer hstream.CloseSend()
			resp, err := hstream.Recv()
			if !assert.NoError(c, err) {
				return
			}
			if !assert.Len(c, resp.GetHosts(), hostCount) {
				return
			}
			var leaders []string
			for _, host := range resp.GetHosts() {
				if host.GetLeader() {
					leaders = append(leaders, host.GetAddress())
				}
			}
			if assert.Len(c, leaders, 1) {
				addr = leaders[0]
			}
		}, time.Second*30, time.Millisecond*100)
		return addr
	}

	schedulerN := func(addr string) int {
		for n, a := range f.cluster.Addresses() {
			if a == addr {
				return n
			}
		}
		require.Failf(t, "unknown scheduler", "no scheduler in the cluster has address %s", addr)
		return -1
	}

	sendAck := func(stream schedulerv1pb.Scheduler_ReportActorTypesClient, order *schedulerv1pb.PlacementOrder) error {
		return stream.Send(&schedulerv1pb.ReportActorTypesRequest{
			Msg: &schedulerv1pb.ReportActorTypesRequest_Ack{Ack: &schedulerv1pb.PlacementOrderAck{
				Operation: order.GetOperation(),
				Seq:       order.GetSeq(),
			}},
		})
	}

	openAndReport := func(client schedulerv1pb.SchedulerClient) schedulerv1pb.Scheduler_ReportActorTypesClient {
		var stream schedulerv1pb.Scheduler_ReportActorTypesClient
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			var err error
			stream, err = client.ReportActorTypes(ctx)
			if !assert.NoError(c, err) {
				return
			}
			err = stream.Send(&schedulerv1pb.ReportActorTypesRequest{
				Msg: &schedulerv1pb.ReportActorTypesRequest_Report{Report: &schedulerv1pb.ActorHost{
					Address:    "127.0.0.1:40001",
					AppId:      "myapp",
					Namespace:  "default",
					ActorTypes: []string{"mytype"},
				}},
			})
			if !assert.NoError(c, err) {
				return
			}
			order, err := stream.Recv()
			if !assert.NoError(c, err) {
				return
			}
			assert.NoError(c, sendAck(stream, order))
			assert.Equal(c, schedulerv1pb.Operation_OPERATION_LOCK, order.GetOperation())
		}, time.Second*30, time.Millisecond*100)
		return stream
	}

	// ackRoundToTable acknowledges every order until the round carrying the
	// host's table completes.
	ackRoundToTable := func(stream schedulerv1pb.Scheduler_ReportActorTypesClient) {
		var tabled bool
		for {
			order, err := stream.Recv()
			require.NoError(t, err)
			require.NoError(t, sendAck(stream, order))
			if order.GetOperation() == schedulerv1pb.Operation_OPERATION_UPDATE {
				table, ok := order.GetTables().GetEntries()["mytype"]
				if ok {
					require.Contains(t, table.GetHosts(), "127.0.0.1:40001")
					tabled = true
				}
			}
			if tabled && order.GetOperation() == schedulerv1pb.Operation_OPERATION_UNLOCK {
				return
			}
		}
	}

	leaderAddr := leaderIn(clients[0], 3)
	leaderN := schedulerN(leaderAddr)
	stream := openAndReport(clients[leaderN])

	// The leader shuts down with the report stream live: the stream is
	// closed telling the sidecar placement is shutting down.
	f.cluster.SchedulerN(t, leaderN).Cleanup(t)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := stream.Recv()
		if !assert.Error(c, err) {
			return
		}
		assert.Equal(c, codes.Unavailable, status.Code(err))
		assert.ErrorContains(c, err, "placement is shutting down")
	}, time.Second*20, time.Millisecond*100)

	// Both remaining schedulers advertise the same new leader, which
	// rebuilds its table from the host that reconnects.
	newLeaderAddr := leaderIn(clients[(leaderN+1)%3], 2)
	require.NotEqual(t, leaderAddr, newLeaderAddr)
	require.Equal(t, newLeaderAddr, leaderIn(clients[(leaderN+2)%3], 2))
	stream = openAndReport(clients[schedulerN(newLeaderAddr)])
	ackRoundToTable(stream)

	// The election picks the lowest address, so the revived original
	// retakes leadership from the interim leader.
	old := f.cluster.SchedulerN(t, leaderN)
	revived := scheduler.New(t,
		scheduler.WithPlacementEnabled(true),
		scheduler.WithID(old.ID()),
		scheduler.WithPort(old.Port()),
		scheduler.WithEtcdClientPort(old.EtcdClientPort()),
		scheduler.WithInitialCluster(old.InitialCluster()),
		scheduler.WithDataDir(old.DataDir()),
	)
	revived.Run(t, ctx)
	t.Cleanup(func() { revived.Cleanup(t) })
	revived.WaitUntilRunning(t, ctx)
	revivedClient := revived.Client(t, ctx)
	openJobs(revivedClient)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := stream.Recv()
		if !assert.Error(c, err) {
			return
		}
		assert.Equal(c, codes.FailedPrecondition, status.Code(err))
		assert.ErrorContains(c, err, "lost placement leadership")
	}, time.Second*30, time.Millisecond*100)

	// The revived original is advertised as leader again and serves the
	// reconnect.
	require.Equal(t, leaderAddr, leaderIn(revivedClient, 3))
	stream = openAndReport(revivedClient)
	ackRoundToTable(stream)
}
