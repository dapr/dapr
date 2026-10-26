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
	suite.Register(new(mixedflags))
}

// mixedflags runs a three scheduler cluster where only one has the placement
// flag, the topology of a rolling update of the flag. The unflagged schedulers
// elect the placement leader among their flagged peers, so they too must
// detect the live placement service and withhold that election.
type mixedflags struct {
	cluster *cluster.Cluster
	place   *placement.Placement
}

func (m *mixedflags) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	m.cluster = cluster.New(t,
		cluster.WithCount(3),
		cluster.WithSchedulerNOptions(0, scheduler.WithPlacementEnabled(true)),
	)
	m.place = placement.New(t)

	return []framework.Option{
		framework.WithProcesses(m.cluster, m.place),
	}
}

func (m *mixedflags) Run(t *testing.T, ctx context.Context) {
	m.cluster.WaitUntilRunning(t, ctx)
	m.place.WaitUntilRunning(t, ctx)

	// A capable sidecar reports the placement address to every scheduler.
	for n := range 3 {
		watch, err := m.cluster.ClientN(t, ctx, n).WatchJobs(ctx)
		require.NoError(t, err)
		require.NoError(t, watch.Send(&schedulerv1pb.WatchJobsRequest{
			WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Initial{
				Initial: &schedulerv1pb.WatchJobsRequestInitial{
					AppId:                      "capable-sidecar",
					Namespace:                  "default",
					SupportsSchedulerPlacement: true,
					PlacementAddresses:         []string{m.place.Address()},
				},
			},
		}))
	}

	hosts := func(n int) (leader, capable bool, ok bool) {
		stream, serr := m.cluster.ClientN(t, ctx, n).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
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

	for n := range 3 {
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			leader, capable, ok := hosts(n)
			if assert.True(c, ok) {
				assert.False(c, leader)
				assert.False(c, capable)
			}
		}, time.Second*10, time.Millisecond*10)
	}

	require.Never(t, func() bool {
		for n := range 3 {
			if leader, _, ok := hosts(n); ok && leader {
				return true
			}
		}
		return false
	}, time.Second*10, time.Millisecond*10,
		"no scheduler may advertise a placement leader while the placement service runs, whichever schedulers carry the flag")

	// Removing the placement service hands placement to the flagged
	// scheduler, and every scheduler broadcasts its leadership.
	m.place.Cleanup(t)
	for n := range 3 {
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			leader, _, ok := hosts(n)
			assert.True(c, ok && leader)
		}, time.Second*15, time.Millisecond*10)
	}
}
