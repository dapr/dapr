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
	suite.Register(new(clustered))
}

// clustered asserts a multi scheduler cluster elects a single placement
// leader: every scheduler advertises the same leader, the leader serves
// report streams, and a follower refuses them with FailedPrecondition.
type clustered struct {
	cluster *cluster.Cluster
}

func (l *clustered) Setup(t *testing.T) []framework.Option {
	l.cluster = cluster.New(t,
		cluster.WithCount(3),
		cluster.WithSchedulerOptions(scheduler.WithPlacementEnabled(true)),
	)
	return []framework.Option{
		framework.WithProcesses(l.cluster),
	}
}

func (l *clustered) Run(t *testing.T, ctx context.Context) {
	l.cluster.WaitUntilRunning(t, ctx)

	// A sidecar connects to every scheduler, so each scheduler's local
	// capability view converges on the cluster view.
	for n := range 3 {
		stream, err := l.cluster.ClientN(t, ctx, n).WatchJobs(ctx)
		require.NoError(t, err)
		require.NoError(t, stream.Send(&schedulerv1pb.WatchJobsRequest{
			WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Initial{Initial: &schedulerv1pb.WatchJobsRequestInitial{
				AppId:                      "capable-sidecar",
				Namespace:                  "default",
				SupportsSchedulerPlacement: true,
			}},
		}))
	}

	// Every scheduler advertises the same single leader.
	var leaderAddr string
	for n := range 3 {
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			hstream, herr := l.cluster.ClientN(t, ctx, n).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
			if !assert.NoError(c, herr) {
				return
			}
			//nolint:errcheck
			defer hstream.CloseSend()
			resp, herr := hstream.Recv()
			if !assert.NoError(c, herr) {
				return
			}
			if !assert.Len(c, resp.GetHosts(), 3) {
				return
			}
			var leaders []string
			for _, host := range resp.GetHosts() {
				assert.True(c, host.GetSchedulerPlacementEnabled())
				if host.GetLeader() {
					leaders = append(leaders, host.GetAddress())
				}
			}
			if !assert.Len(c, leaders, 1) {
				return
			}
			if leaderAddr == "" {
				leaderAddr = leaders[0]
			}
			assert.Equal(c, leaderAddr, leaders[0])
		}, time.Second*30, time.Millisecond*100)
	}

	leaderN := -1
	for n, addr := range l.cluster.Addresses() {
		if addr == leaderAddr {
			leaderN = n
		}
	}
	require.NotEqual(t, -1, leaderN, "the leader must be one of the cluster's schedulers")

	// The leader serves the report stream.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		rstream, rerr := l.cluster.ClientN(t, ctx, leaderN).ReportActorTypes(ctx)
		if !assert.NoError(c, rerr) {
			return
		}
		rerr = rstream.Send(&schedulerv1pb.ReportActorTypesRequest{
			Msg: &schedulerv1pb.ReportActorTypesRequest_Report{Report: &schedulerv1pb.ActorHost{
				Address:    "127.0.0.1:40001",
				AppId:      "myapp",
				Namespace:  "default",
				ActorTypes: []string{"mytype"},
			}},
		})
		if !assert.NoError(c, rerr) {
			return
		}
		order, rerr := rstream.Recv()
		if !assert.NoError(c, rerr) {
			return
		}
		assert.Equal(c, schedulerv1pb.Operation_OPERATION_LOCK, order.GetOperation())
	}, time.Second*20, time.Millisecond*100)

	// A follower refuses report streams.
	followerN := (leaderN + 1) % 3
	fstream, err := l.cluster.ClientN(t, ctx, followerN).ReportActorTypes(ctx)
	require.NoError(t, err)
	_, err = fstream.Recv()
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}
