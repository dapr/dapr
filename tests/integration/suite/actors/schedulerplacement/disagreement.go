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

package schedulerplacement

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/cluster"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(disagreement))
}

// disagreement gives only scheduler-0 the placement service's address, so
// the schedulers disagree about presence: scheduler-0 detects it, the others
// never look. No scheduler may advertise a placement leader out of that
// disagreement while the placement service runs.
type disagreement struct {
	cluster *cluster.Cluster
	place   *placement.Placement
}

func (d *disagreement) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	d.cluster = cluster.New(t,
		cluster.WithCount(3),
		cluster.WithSchedulerOptions(scheduler.WithPlacementEnabled(true)),
	)
	d.place = placement.New(t)

	return []framework.Option{
		framework.WithProcesses(d.cluster, d.place),
	}
}

func (d *disagreement) Run(t *testing.T, ctx context.Context) {
	d.cluster.WaitUntilRunning(t, ctx)
	d.place.WaitUntilRunning(t, ctx)

	// A capable sidecar reports the placement address to scheduler-0 alone.
	watch, err := d.cluster.ClientN(t, ctx, 0).WatchJobs(ctx)
	require.NoError(t, err)
	require.NoError(t, watch.Send(&schedulerv1pb.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Initial{
			Initial: &schedulerv1pb.WatchJobsRequestInitial{
				AppId:                      "capable-sidecar",
				Namespace:                  "default",
				SupportsSchedulerPlacement: true,
				PlacementAddresses:         []string{d.place.Address()},
			},
		},
	}))

	clients := make([]schedulerv1pb.SchedulerClient, 3)
	for n := range clients {
		clients[n] = d.cluster.ClientN(t, ctx, n)
	}

	hosts := func(n int) (leader, capable bool, ok bool) {
		stream, serr := clients[n].WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
		if serr != nil {
			return false, false, false
		}
		//nolint:errcheck
		defer stream.CloseSend()
		resp, serr := stream.Recv()
		if serr != nil {
			return false, false, false
		}
		for _, host := range resp.GetHosts() {
			leader = leader || host.GetLeader()
			capable = capable || host.GetSchedulerPlacementEnabled()
		}
		return leader, capable, true
	}

	// Scheduler-0's probe has answered once it masks the capability bit, so
	// the withhold below is settled state, not a boot race.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		leader, capable, ok := hosts(0)
		if assert.True(c, ok) {
			assert.False(c, leader)
			assert.False(c, capable)
		}
	}, time.Second*20, time.Millisecond*10)

	// The schedulers disagree about presence, yet none may advertise:
	// scheduler-0 withholds for the placement service, the others for want
	// of a capable sidecar of their own.
	require.Never(t, func() bool {
		for n := range 3 {
			if leader, _, ok := hosts(n); ok && leader {
				return true
			}
		}
		return false
	}, time.Second*10, time.Millisecond*10,
		"no scheduler may advertise a placement leader while the placement service runs")
}
