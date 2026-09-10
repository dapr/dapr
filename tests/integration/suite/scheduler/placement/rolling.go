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
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/cluster"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(rolling))
}

// rolling runs a cluster where placement is enabled on two of the three
// schedulers, as during a rolling upgrade: the election skips the disabled
// scheduler, however its address is the lowest, the elected enabled scheduler
// serves report streams, and the disabled one refuses them with
// Unimplemented.
type rolling struct {
	cluster *cluster.Cluster
}

func (r *rolling) Setup(t *testing.T) []framework.Option {
	// The disabled scheduler broadcasts the lowest address, so an election
	// ignoring the placement enabled bit would pick it.
	r.cluster = cluster.New(t,
		cluster.WithCount(3),
		cluster.WithOverrideBroadcastHostPorts("127.0.0.1:40012", "127.0.0.1:40011", "127.0.0.1:40010"),
		cluster.WithSchedulerNOptions(0, scheduler.WithPlacementEnabled(true)),
		cluster.WithSchedulerNOptions(1, scheduler.WithPlacementEnabled(true)),
	)
	return []framework.Option{
		framework.WithProcesses(r.cluster),
	}
}

func (r *rolling) Run(t *testing.T, ctx context.Context) {
	r.cluster.WaitUntilRunning(t, ctx)

	// The watches open before any capable sidecar connects, so every
	// broadcast up to and including the leader advertisement is observed.
	wctx, wcancel := context.WithTimeout(ctx, time.Second*20)
	defer wcancel()
	clients := make([]schedulerv1pb.SchedulerClient, 3)
	watches := make([]schedulerv1pb.Scheduler_WatchHostsClient, 3)
	for n := range 3 {
		clients[n] = r.cluster.ClientN(t, ctx, n)
		var err error
		watches[n], err = clients[n].WatchHosts(wctx, new(schedulerv1pb.WatchHostsRequest))
		require.NoError(t, err)
	}

	for n := range 3 {
		stream, err := clients[n].WatchJobs(ctx)
		require.NoError(t, err)
		require.NoError(t, stream.Send(&schedulerv1pb.WatchJobsRequest{
			WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Initial{Initial: &schedulerv1pb.WatchJobsRequestInitial{
				AppId:                      "capable-sidecar",
				Namespace:                  "default",
				SupportsSchedulerPlacement: true,
			}},
		}))
	}

	// No broadcast ever advertises the disabled scheduler as leader, and
	// every scheduler converges on the lowest addressed placement enabled
	// scheduler.
	for n := range 3 {
		for {
			resp, err := watches[n].Recv()
			require.NoError(t, err)
			var leaders []string
			for _, host := range resp.GetHosts() {
				if host.GetLeader() {
					leaders = append(leaders, host.GetAddress())
				}
				if host.GetAddress() == "127.0.0.1:40010" {
					assert.False(t, host.GetSchedulerPlacementEnabled())
					require.False(t, host.GetLeader(), "the disabled scheduler must never be advertised as leader")
				}
			}
			if len(resp.GetHosts()) != 3 || len(leaders) == 0 {
				continue
			}
			require.Equal(t, []string{"127.0.0.1:40011"}, leaders)
			for _, host := range resp.GetHosts() {
				if host.GetAddress() != "127.0.0.1:40010" {
					assert.True(t, host.GetSchedulerPlacementEnabled())
				}
			}
			break
		}
	}

	// The elected leader serves the report stream.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		stream, err := clients[1].ReportActorTypes(ctx)
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
		assert.Equal(c, schedulerv1pb.Operation_OPERATION_LOCK, order.GetOperation())
	}, time.Second*20, time.Millisecond*100)

	// The disabled scheduler refuses report streams.
	dstream, err := clients[2].ReportActorTypes(ctx)
	require.NoError(t, err)
	_, err = dstream.Recv()
	require.Equal(t, codes.Unimplemented, status.Code(err))
}
